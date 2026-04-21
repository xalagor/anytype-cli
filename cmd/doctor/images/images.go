package images

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"

	"github.com/spf13/cobra"

	"github.com/anyproto/anytype-cli/core"
	"github.com/anyproto/anytype-cli/core/output"
)

const sourceRelObjectId = "bafyreiciy7gpgdnsb2s3qdgvanvsksxkhbczlzf63vnhazzk2sqcyco2xu"

var pinterestNameRe = regexp.MustCompile(`^pinterest_(\d+)$`)

func NewImagesCmd() *cobra.Command {
	var (
		spaceId      string
		taggerFlag   bool
		namesFlag    bool
		florenceFlag bool
		relationName string
		pythonExe    string
		genThresh    float64
		charThresh   float64
		dryRun       bool
		limit        int
		// Florence-specific flags
		florenceRelation string
		florenceTask     string
		florenceModel    string
	)

	cmd := &cobra.Command{
		Use:   "images",
		Short: "Doctor commands for image objects",
		Long:  "Run diagnostics and enrichment operations on image objects in your Anytype spaces",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !taggerFlag && !namesFlag && !florenceFlag {
				return cmd.Help()
			}
			if taggerFlag && florenceFlag {
				return fmt.Errorf("--tagger and --florence are mutually exclusive")
			}
			if namesFlag {
				return runNames(spaceId, dryRun, limit)
			}
			if florenceFlag {
				return runFlorence(spaceId, florenceRelation, pythonExe, florenceTask, florenceModel, dryRun, limit)
			}
			return runTagger(spaceId, relationName, pythonExe, genThresh, charThresh, dryRun, limit)
		},
	}

	cmd.Flags().BoolVar(&taggerFlag, "tagger", false, "Tag images with WD EVA02 large v3 tagger (requires Python + onnxruntime + Pillow + huggingface_hub)")
	cmd.Flags().BoolVar(&namesFlag, "names", false, "Restore Source | URL for images whose name matches pinterest_<id>")
	cmd.Flags().BoolVar(&florenceFlag, "florence", false, "Generate image descriptions with Microsoft Florence-2 (requires Python + torch + transformers + Pillow + einops)")
	cmd.Flags().StringVar(&spaceId, "space", "", "Space `id` to process (default: all spaces)")
	cmd.Flags().StringVar(&relationName, "relation", "WD14 Tagger", "Display name of the relation to store tags in (used with --tagger)")
	cmd.Flags().StringVar(&pythonExe, "python", "python3", "Python executable to use")
	cmd.Flags().Float64Var(&genThresh, "threshold", 0.35, "General tag confidence threshold (0–1)")
	cmd.Flags().Float64Var(&charThresh, "char-threshold", 0.85, "Character tag confidence threshold (0–1)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print results without writing them to Anytype")
	cmd.Flags().IntVar(&limit, "limit", 0, "Max number of images to process (0 = no limit, useful for testing)")
	cmd.Flags().StringVar(&florenceRelation, "florence-relation", "Florence 2 Caption", "Display name of the relation to store Florence-2 descriptions in")
	cmd.Flags().StringVar(&florenceTask, "florence-task", "detailed", "Florence-2 captioning task: caption | detailed | more-detailed")
	cmd.Flags().StringVar(&florenceModel, "florence-model", "microsoft/Florence-2-base", "HuggingFace model ID for Florence-2 (e.g. microsoft/Florence-2-large)")

	return cmd
}

func runTagger(spaceId, relationName, pythonExe string, genThresh, charThresh float64, dryRun bool, limit int) error {
	// Gather spaces to process.
	var spaceIds []string
	if spaceId != "" {
		spaceIds = []string{spaceId}
	} else {
		spaces, err := core.ListSpaces()
		if err != nil {
			return output.Error("Failed to list spaces: %w", err)
		}
		for _, s := range spaces {
			spaceIds = append(spaceIds, s.SpaceId)
		}
	}

	if len(spaceIds) == 0 {
		output.Info("No spaces found")
		return nil
	}

	// Resolve the Python executable (handles python vs python3 on Windows).
	pythonExe, err := core.ResolvePython(pythonExe)
	if err != nil {
		return output.Error("%v", err)
	}

	// Write the embedded Python tagger script once.
	scriptPath, err := core.WriteWdTaggerScript()
	if err != nil {
		return output.Error("Failed to prepare tagger script: %w", err)
	}

	// Temporary directory for downloaded images.
	tempDir, err := os.MkdirTemp("", "anytype-tagger-*")
	if err != nil {
		return output.Error("Failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Discover the gateway HTTP URL by opening the first available space.
	// The gateway is an account-wide HTTP server; its port is dynamic (starts at 47800).
	gatewayURL, err := core.GetGatewayURL(spaceIds[0])
	if err != nil {
		return output.Error("Failed to get gateway URL: %w", err)
	}

	// Start the persistent tagger process. The model is loaded here once;
	// all images in every space share the same running process.
	output.Info("Loading WD EVA02 large v3 model (downloading on first run — this may take a few minutes)…")
	tagger, err := core.StartWdTaggerServer(pythonExe, scriptPath, genThresh, charThresh)
	if err != nil {
		return output.Error("Failed to start tagger: %w", err)
	}
	defer tagger.Close()
	output.Info("Tagger ready.")

	var totalImages, totalTagged, totalSkipped int
	done := false

	for _, sid := range spaceIds {
		if done {
			break
		}

		// Resolve the custom relation key from its display name.
		relKey, err := core.FindRelationKeyByName(sid, relationName)
		if err != nil {
			output.Warning("Space %s: %v — skipping", sid, err)
			continue
		}

		// Find images that have no tags yet.
		images, err := core.FindUntaggedImages(sid, relKey)
		if err != nil {
			output.Warning("Space %s: failed to find images: %v", sid, err)
			continue
		}

		if len(images) == 0 {
			output.Info("Space %s: all images already tagged", sid)
			continue
		}

		// Apply limit across all spaces.
		if limit > 0 {
			remaining := limit - totalImages
			if remaining <= 0 {
				done = true
				break
			}
			if len(images) > remaining {
				images = images[:remaining]
			}
		}

		output.Info("Space %s: tagging %d image(s) (relation key: %s)", sid, len(images), relKey)
		totalImages += len(images)

		for _, img := range images {
			label := img.Name
			if label == "" {
				label = img.ObjectId
			}
			link := objectDeepLink(img.ObjectId, sid)

			// Download via the HTTP gateway. The file is saved as <objectId><ext>,
			// so special characters in the original name never touch the filesystem.
			localPath, err := core.DownloadImageViaGateway(img.ObjectId, tempDir, gatewayURL)
			if err != nil {
				if errors.Is(err, core.ErrImageFormatNotSupported) {
					output.Info("  skip %s (format not supported by tagger)\n    %s", label, link)
				} else {
					output.Warning("  skip %s\n    %s\n    %v", label, link, err)
				}
				totalSkipped++
				continue
			}

			// Send the image to the already-running tagger process.
			tags, err := tagger.TagImage(localPath)
			os.Remove(localPath)
			if err != nil {
				output.Warning("  skip %s\n    %s\n    %v", label, link, err)
				totalSkipped++
				continue
			}

			if tags == "" {
				output.Warning("  skip %s (no tags produced)\n    %s", label, link)
				totalSkipped++
				continue
			}

			if dryRun {
				output.Info("  [dry-run] %s\n    %s\n    -> %s", label, link, truncate(tags, 120))
				totalTagged++
				continue
			}

			// Write the tags into the existing Anytype object's relation field.
			// The image itself is never re-uploaded; only this metadata field changes.
			if err := core.SetObjectTextRelation(img.ObjectId, relKey, tags); err != nil {
				output.Warning("  skip %s\n    %s\n    failed to save: %v", label, link, err)
				totalSkipped++
				continue
			}

			output.Success("  %s\n    %s\n    %s", label, link, truncate(tags, 120))
			totalTagged++
		}
	}

	fmt.Println()
	if dryRun {
		output.Info("Dry run: %d/%d images would be tagged, %d skipped", totalTagged, totalImages, totalSkipped)
	} else {
		output.Info("Done: %d tagged, %d skipped (of %d found)", totalTagged, totalSkipped, totalImages)
	}
	return nil
}

func runNames(spaceId string, dryRun bool, limit int) error {
	var spaceIds []string
	if spaceId != "" {
		spaceIds = []string{spaceId}
	} else {
		spaces, err := core.ListSpaces()
		if err != nil {
			return output.Error("Failed to list spaces: %w", err)
		}
		for _, s := range spaces {
			spaceIds = append(spaceIds, s.SpaceId)
		}
	}

	if len(spaceIds) == 0 {
		output.Info("No spaces found")
		return nil
	}

	var totalFound, totalRestored, totalSkipped int
	done := false

	for _, sid := range spaceIds {
		if done {
			break
		}

		sourceRelKey, err := core.FindRelationKeyByObjectId(sid, sourceRelObjectId)
		if err != nil {
			output.Warning("Space %s: %v — skipping", sid, err)
			continue
		}

		images, err := core.FindImagesWithEmptyRelation(sid, sourceRelKey)
		if err != nil {
			output.Warning("Space %s: failed to find images: %v", sid, err)
			continue
		}

		// Filter to pinterest_ names only.
		var matches []core.ImageObject
		for _, img := range images {
			if pinterestNameRe.MatchString(img.Name) {
				matches = append(matches, img)
			}
		}

		if len(matches) == 0 {
			output.Info("Space %s: no pinterest images without Source URL", sid)
			continue
		}

		if limit > 0 {
			remaining := limit - totalFound
			if remaining <= 0 {
				done = true
				break
			}
			if len(matches) > remaining {
				matches = matches[:remaining]
			}
		}

		output.Info("Space %s: found %d pinterest image(s) to restore", sid, len(matches))
		totalFound += len(matches)

		for _, img := range matches {
			m := pinterestNameRe.FindStringSubmatch(img.Name)
			pinURL := "https://www.pinterest.com/pin/" + m[1] + "/"
			link := objectDeepLink(img.ObjectId, sid)

			if dryRun {
				output.Info("  [dry-run] %s\n    %s\n    -> %s", img.Name, link, pinURL)
				totalRestored++
				continue
			}

			if err := core.SetObjectTextRelation(img.ObjectId, sourceRelKey, pinURL); err != nil {
				output.Warning("  skip %s\n    %s\n    failed to save: %v", img.Name, link, err)
				totalSkipped++
				continue
			}

			output.Success("  %s\n    %s\n    -> %s", img.Name, link, pinURL)
			totalRestored++
		}
	}

	fmt.Println()
	if dryRun {
		output.Info("Dry run: %d/%d images would be restored, %d skipped", totalRestored, totalFound, totalSkipped)
	} else {
		output.Info("Done: %d restored, %d skipped (of %d found)", totalRestored, totalSkipped, totalFound)
	}
	return nil
}

func runFlorence(spaceId, relationName, pythonExe, task, modelId string, dryRun bool, limit int) error {
	var spaceIds []string
	if spaceId != "" {
		spaceIds = []string{spaceId}
	} else {
		spaces, err := core.ListSpaces()
		if err != nil {
			return output.Error("Failed to list spaces: %w", err)
		}
		for _, s := range spaces {
			spaceIds = append(spaceIds, s.SpaceId)
		}
	}

	if len(spaceIds) == 0 {
		output.Info("No spaces found")
		return nil
	}

	pythonExe, err := core.ResolvePython(pythonExe)
	if err != nil {
		return output.Error("%v", err)
	}

	scriptPath, err := core.WriteFlorenceScript()
	if err != nil {
		return output.Error("Failed to prepare Florence script: %w", err)
	}

	tempDir, err := os.MkdirTemp("", "anytype-florence-*")
	if err != nil {
		return output.Error("Failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	gatewayURL, err := core.GetGatewayURL(spaceIds[0])
	if err != nil {
		return output.Error("Failed to get gateway URL: %w", err)
	}

	output.Info("Loading Florence-2 model %s (downloading on first run — this may take a few minutes)…", modelId)
	florence, err := core.StartFlorenceServer(pythonExe, scriptPath, task, modelId)
	if err != nil {
		return output.Error("Failed to start Florence-2: %w", err)
	}
	defer florence.Close()
	output.Info("Florence-2 ready.")

	var totalImages, totalDescribed, totalSkipped int
	done := false

	for _, sid := range spaceIds {
		if done {
			break
		}

		relKey, err := core.FindRelationKeyByName(sid, relationName)
		if err != nil {
			output.Warning("Space %s: %v — skipping", sid, err)
			continue
		}

		images, err := core.FindUntaggedImages(sid, relKey)
		if err != nil {
			output.Warning("Space %s: failed to find images: %v", sid, err)
			continue
		}

		if len(images) == 0 {
			output.Info("Space %s: all images already have a description", sid)
			continue
		}

		if limit > 0 {
			remaining := limit - totalImages
			if remaining <= 0 {
				done = true
				break
			}
			if len(images) > remaining {
				images = images[:remaining]
			}
		}

		output.Info("Space %s: describing %d image(s) (relation key: %s)", sid, len(images), relKey)
		totalImages += len(images)

		for _, img := range images {
			label := img.Name
			if label == "" {
				label = img.ObjectId
			}
			link := objectDeepLink(img.ObjectId, sid)

			localPath, err := core.DownloadImageViaGateway(img.ObjectId, tempDir, gatewayURL)
			if err != nil {
				if errors.Is(err, core.ErrImageFormatNotSupported) {
					output.Info("  skip %s (format not supported by Florence-2)\n    %s", label, link)
				} else {
					output.Warning("  skip %s\n    %s\n    %v", label, link, err)
				}
				totalSkipped++
				continue
			}

			description, err := florence.DescribeImage(localPath)
			os.Remove(localPath)
			if err != nil {
				output.Warning("  skip %s\n    %s\n    %v", label, link, err)
				totalSkipped++
				continue
			}

			if description == "" {
				output.Warning("  skip %s (no description produced)\n    %s", label, link)
				totalSkipped++
				continue
			}

			if dryRun {
				output.Info("  [dry-run] %s\n    %s\n    -> %s", label, link, truncate(description, 120))
				totalDescribed++
				continue
			}

			if err := core.SetObjectTextRelation(img.ObjectId, relKey, description); err != nil {
				output.Warning("  skip %s\n    %s\n    failed to save: %v", label, link, err)
				totalSkipped++
				continue
			}

			output.Success("  %s\n    %s\n    %s", label, link, truncate(description, 120))
			totalDescribed++
		}
	}

	fmt.Println()
	if dryRun {
		output.Info("Dry run: %d/%d images would be described, %d skipped", totalDescribed, totalImages, totalSkipped)
	} else {
		output.Info("Done: %d described, %d skipped (of %d found)", totalDescribed, totalSkipped, totalImages)
	}
	return nil
}

// objectDeepLink returns an anytype:// deep link for opening the object in the desktop client.
func objectDeepLink(objectId, spaceId string) string {
	q := url.Values{}
	q.Set("objectId", objectId)
	q.Set("spaceId", spaceId)
	return "anytype://object?" + q.Encode()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
