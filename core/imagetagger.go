package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/anyproto/anytype-heart/pb"
	"github.com/anyproto/anytype-heart/pb/service"
	"github.com/anyproto/anytype-heart/pkg/lib/bundle"
	"github.com/anyproto/anytype-heart/pkg/lib/pb/model"
	"github.com/anyproto/anytype-heart/util/pbtypes"
)

// ImageObject holds metadata for an Anytype image object.
type ImageObject struct {
	ObjectId string
	Name     string
	SpaceId  string
}

// FindRelationKeyByName searches the given space for a relation whose display name
// matches name and returns its unique relation key (e.g. "rel-xxxxxxxx").
func FindRelationKeyByName(spaceId, name string) (string, error) {
	var key string
	err := GRPCCall(func(ctx context.Context, client service.ClientCommandsClient) error {
		req := &pb.RpcObjectSearchRequest{
			SpaceId: spaceId,
			Filters: []*model.BlockContentDataviewFilter{
				{
					RelationKey: bundle.RelationKeyResolvedLayout.String(),
					Condition:   model.BlockContentDataviewFilter_Equal,
					Value:       pbtypes.Int64(int64(model.ObjectType_relation)),
				},
				{
					RelationKey: bundle.RelationKeyName.String(),
					Condition:   model.BlockContentDataviewFilter_Equal,
					Value:       pbtypes.String(name),
				},
			},
			Keys: []string{
				bundle.RelationKeyId.String(),
				bundle.RelationKeyRelationKey.String(),
			},
		}
		resp, err := client.ObjectSearch(ctx, req)
		if err != nil {
			return fmt.Errorf("search relations: %w", err)
		}
		if resp.Error != nil && resp.Error.Code != pb.RpcObjectSearchResponseError_NULL {
			return fmt.Errorf("search error: %s", resp.Error.Description)
		}
		for _, record := range resp.Records {
			rk := pbtypes.GetString(record, bundle.RelationKeyRelationKey.String())
			if rk != "" {
				key = rk
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if key == "" {
		return "", fmt.Errorf("relation %q not found in space %s", name, spaceId)
	}
	return key, nil
}

// FindUntaggedImages returns image objects in the space where the given relation key is empty.
func FindUntaggedImages(spaceId, relationKey string) ([]ImageObject, error) {
	var images []ImageObject
	err := GRPCCall(func(ctx context.Context, client service.ClientCommandsClient) error {
		req := &pb.RpcObjectSearchRequest{
			SpaceId: spaceId,
			Filters: []*model.BlockContentDataviewFilter{
				{
					RelationKey: bundle.RelationKeyResolvedLayout.String(),
					Condition:   model.BlockContentDataviewFilter_Equal,
					Value:       pbtypes.Int64(int64(model.ObjectType_image)),
				},
				{
					RelationKey: relationKey,
					Condition:   model.BlockContentDataviewFilter_Empty,
				},
				{
					RelationKey: bundle.RelationKeyIsHidden.String(),
					Condition:   model.BlockContentDataviewFilter_Equal,
					Value:       pbtypes.Bool(false),
				},
				{
					RelationKey: bundle.RelationKeyIsArchived.String(),
					Condition:   model.BlockContentDataviewFilter_Equal,
					Value:       pbtypes.Bool(false),
				},
				{
					RelationKey: bundle.RelationKeyIsDeleted.String(),
					Condition:   model.BlockContentDataviewFilter_Equal,
					Value:       pbtypes.Bool(false),
				},
			},
			Keys: []string{
				bundle.RelationKeyId.String(),
				bundle.RelationKeyName.String(),
			},
		}
		resp, err := client.ObjectSearch(ctx, req)
		if err != nil {
			return fmt.Errorf("search images: %w", err)
		}
		if resp.Error != nil && resp.Error.Code != pb.RpcObjectSearchResponseError_NULL {
			return fmt.Errorf("search error: %s", resp.Error.Description)
		}
		for _, record := range resp.Records {
			id := pbtypes.GetString(record, bundle.RelationKeyId.String())
			name := pbtypes.GetString(record, bundle.RelationKeyName.String())
			if id != "" {
				images = append(images, ImageObject{ObjectId: id, Name: name, SpaceId: spaceId})
			}
		}
		return nil
	})
	return images, err
}

// DownloadImageToDir downloads an image object to the given directory.
// Returns the local path where the file was saved.
func DownloadImageToDir(objectId, dir string) (string, error) {
	var localPath string
	err := GRPCCall(func(ctx context.Context, client service.ClientCommandsClient) error {
		req := &pb.RpcFileDownloadRequest{
			ObjectId: objectId,
			Path:     dir,
		}
		resp, err := client.FileDownload(ctx, req)
		if err != nil {
			return fmt.Errorf("download image %s: %w", objectId, err)
		}
		if resp.Error != nil && resp.Error.Code != pb.RpcFileDownloadResponseError_NULL {
			return fmt.Errorf("download error: %s", resp.Error.Description)
		}
		localPath = resp.LocalPath
		return nil
	})
	return localPath, err
}

// SetObjectTextRelation sets a text relation value on an Anytype object.
func SetObjectTextRelation(objectId, relationKey, value string) error {
	return GRPCCall(func(ctx context.Context, client service.ClientCommandsClient) error {
		req := &pb.RpcObjectSetDetailsRequest{
			ContextId: objectId,
			Details: []*model.Detail{
				{
					Key:   relationKey,
					Value: pbtypes.String(value),
				},
			},
		}
		resp, err := client.ObjectSetDetails(ctx, req)
		if err != nil {
			return fmt.Errorf("set relation on %s: %w", objectId, err)
		}
		if resp.Error != nil && resp.Error.Code != pb.RpcObjectSetDetailsResponseError_NULL {
			return fmt.Errorf("set relation error: %s", resp.Error.Description)
		}
		return nil
	})
}

// wdTaggerScript is a minimal Python script for WD14 ViT v3 tagging.
// Usage: python wdtagger.py <image_path> [general_thresh] [char_thresh]
// Outputs comma-separated tags to stdout.
const wdTaggerScript = `#!/usr/bin/env python3
"""WD14 ViT v3 single-image tagger for anytype-cli."""
import sys
import csv
import numpy as np
from PIL import Image
from huggingface_hub import hf_hub_download
import onnxruntime as ort

MODEL_REPO = "SmilingWolf/wd-vit-tagger-v3"

def load_model():
    model_path = hf_hub_download(MODEL_REPO, "model.onnx")
    tags_path = hf_hub_download(MODEL_REPO, "selected_tags.csv")
    sess = ort.InferenceSession(model_path, providers=["CPUExecutionProvider"])
    target_size = int(sess.get_inputs()[0].shape[2])
    tags = []
    with open(tags_path, newline="", encoding="utf-8") as f:
        for row in csv.DictReader(f):
            tags.append(row)
    return sess, tags, target_size

def preprocess(img_path, size):
    img = Image.open(img_path).convert("RGBA")
    bg = Image.new("RGBA", img.size, (255, 255, 255, 255))
    bg.paste(img, mask=img.split()[3])
    img = bg.convert("RGB")
    w, h = img.size
    m = max(w, h)
    pad = Image.new("RGB", (m, m), (255, 255, 255))
    pad.paste(img, ((m - w) // 2, (m - h) // 2))
    pad = pad.resize((size, size), Image.BICUBIC)
    arr = np.array(pad, dtype=np.float32)[:, :, ::-1]  # RGB -> BGR
    return np.expand_dims(arr, 0)

def main():
    if len(sys.argv) < 2:
        print("Usage: wdtagger.py <image> [general_thresh] [char_thresh]", file=sys.stderr)
        sys.exit(1)

    img_path = sys.argv[1]
    gen_thresh = float(sys.argv[2]) if len(sys.argv) > 2 else 0.35
    char_thresh = float(sys.argv[3]) if len(sys.argv) > 3 else 0.85

    sess, tags, size = load_model()
    arr = preprocess(img_path, size)
    preds = sess.run(None, {sess.get_inputs()[0].name: arr})[0][0]

    result = []
    for i, row in enumerate(tags):
        if i >= len(preds):
            break
        try:
            cat = int(row.get("category", 9))
        except (ValueError, TypeError):
            cat = 9
        if cat == 9:  # skip rating tags
            continue
        thresh = char_thresh if cat == 4 else gen_thresh
        if float(preds[i]) >= thresh:
            result.append(row["name"].replace("_", " "))

    print(", ".join(result))

if __name__ == "__main__":
    main()
`

// WriteWdTaggerScript writes the embedded Python tagger script to a temp file
// and returns the path. The file is reused across calls within a process.
func WriteWdTaggerScript() (string, error) {
	scriptPath := filepath.Join(os.TempDir(), "anytype_wdtagger.py")
	if err := os.WriteFile(scriptPath, []byte(wdTaggerScript), 0o644); err != nil {
		return "", fmt.Errorf("write tagger script: %w", err)
	}
	return scriptPath, nil
}

// ResolvePython returns the first Python executable found in PATH.
// If the user supplied an explicit name (not "python3"), that is used as-is.
// Otherwise we try "python3" then "python" to handle Windows where only
// "python" is typically available.
func ResolvePython(requested string) (string, error) {
	if requested != "python3" {
		// User explicitly overrode the default — trust them.
		if _, err := exec.LookPath(requested); err != nil {
			return "", fmt.Errorf("%q not found in PATH", requested)
		}
		return requested, nil
	}
	for _, candidate := range []string{"python3", "python"} {
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no Python interpreter found in PATH (tried python3, python); install Python or pass --python")
}

// RunWdTagger invokes the Python tagger script on a single image and returns
// the comma-separated tag string.
func RunWdTagger(python, scriptPath, imagePath string, generalThresh, charThresh float64) (string, error) {
	cmd := exec.Command(
		python, scriptPath, imagePath,
		fmt.Sprintf("%.2f", generalThresh),
		fmt.Sprintf("%.2f", charThresh),
	)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := strings.TrimSpace(string(exitErr.Stderr))
			if stderr != "" {
				return "", fmt.Errorf("tagger error: %s", stderr)
			}
		}
		return "", fmt.Errorf("tagger error: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
