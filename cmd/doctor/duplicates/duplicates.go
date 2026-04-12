package duplicates

import (
	"github.com/spf13/cobra"

	"github.com/anyproto/anytype-cli/core"
	"github.com/anyproto/anytype-cli/core/output"
)

func NewDuplicatesCmd() *cobra.Command {
	var spaceId string

	cmd := &cobra.Command{
		Use:   "duplicates",
		Short: "Find objects with duplicate names",
		Long:  "Search for objects that share the same name within a space or across all spaces",
		RunE: func(cmd *cobra.Command, args []string) error {
			if spaceId != "" {
				return runDuplicates([]string{spaceId}, false)
			}

			spaces, err := core.ListSpaces()
			if err != nil {
				return output.Error("Failed to list spaces: %w", err)
			}
			if len(spaces) == 0 {
				output.Info("No spaces found")
				return nil
			}

			ids := make([]string, 0, len(spaces))
			for _, s := range spaces {
				ids = append(ids, s.SpaceId)
			}
			return runDuplicates(ids, true)
		},
	}

	cmd.Flags().StringVar(&spaceId, "space", "", "Space `id` to check (default: all spaces)")

	return cmd
}

// runDuplicates executes the duplicate search across the given space IDs and prints results.
// showSpaceId controls whether the space ID header is printed (useful for multi-space runs).
func runDuplicates(spaceIds []string, showSpaceId bool) error {
	totalGroups := 0

	for _, id := range spaceIds {
		groups, err := core.FindDuplicateNames(id)
		if err != nil {
			output.Warning("Failed to check space %s: %v", id, err)
			continue
		}

		for _, g := range groups {
			totalGroups++
			if showSpaceId {
				output.Info("Space: %s", g.SpaceId)
			}
			output.Info("  Name: %q  (%d copies)", g.Name, len(g.ObjectIDs))
			for _, objId := range g.ObjectIDs {
				output.Info("    - %s", objId)
			}
		}
	}

	if totalGroups == 0 {
		output.Success("No duplicate names found")
		return nil
	}

	output.Warning("Found %d group(s) of duplicate names", totalGroups)
	return nil
}
