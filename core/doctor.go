package core

import (
	"context"
	"fmt"
	"sort"

	"github.com/anyproto/anytype-heart/pb"
	"github.com/anyproto/anytype-heart/pb/service"
	"github.com/anyproto/anytype-heart/pkg/lib/bundle"
	"github.com/anyproto/anytype-heart/pkg/lib/pb/model"
	"github.com/anyproto/anytype-heart/util/pbtypes"
)

// DuplicateGroup holds a name shared by multiple objects and their IDs.
type DuplicateGroup struct {
	Name      string
	ObjectIDs []string
	SpaceId   string
}

// FindDuplicateNames searches for objects with duplicate names in the given space.
// Only user-facing content objects are considered (not templates, relations, system types, etc.).
// Archived, hidden, and deleted objects are excluded.
func FindDuplicateNames(spaceId string) ([]DuplicateGroup, error) {
	var groups []DuplicateGroup

	err := GRPCCall(func(ctx context.Context, client service.ClientCommandsClient) error {
		req := &pb.RpcObjectSearchRequest{
			SpaceId: spaceId,
			Filters: []*model.BlockContentDataviewFilter{
				{
					RelationKey: bundle.RelationKeyResolvedLayout.String(),
					Condition:   model.BlockContentDataviewFilter_In,
					Value: pbtypes.IntList(
						int(model.ObjectType_basic),
						int(model.ObjectType_profile),
						int(model.ObjectType_todo),
						int(model.ObjectType_note),
						int(model.ObjectType_bookmark),
						int(model.ObjectType_set),
						int(model.ObjectType_collection),
					),
				},
				{
					RelationKey: bundle.RelationKeyIsHidden.String(),
					Condition:   model.BlockContentDataviewFilter_NotEqual,
					Value:       pbtypes.Bool(true),
				},
				{
					RelationKey: bundle.RelationKeyIsArchived.String(),
					Condition:   model.BlockContentDataviewFilter_NotEqual,
					Value:       pbtypes.Bool(true),
				},
				{
					RelationKey: bundle.RelationKeyIsDeleted.String(),
					Condition:   model.BlockContentDataviewFilter_NotEqual,
					Value:       pbtypes.Bool(true),
				},
				{
					RelationKey: bundle.RelationKeyName.String(),
					Condition:   model.BlockContentDataviewFilter_NotEmpty,
				},
			},
			Keys: []string{
				bundle.RelationKeyId.String(),
				bundle.RelationKeyName.String(),
			},
		}

		resp, err := client.ObjectSearch(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to search objects in space %s: %w", spaceId, err)
		}
		if resp.Error != nil && resp.Error.Code != pb.RpcObjectSearchResponseError_NULL {
			return fmt.Errorf("object search error: %s", resp.Error.Description)
		}

		nameToIDs := make(map[string][]string)
		for _, record := range resp.Records {
			name := pbtypes.GetString(record, bundle.RelationKeyName.String())
			id := pbtypes.GetString(record, bundle.RelationKeyId.String())
			if name == "" || id == "" {
				continue
			}
			nameToIDs[name] = append(nameToIDs[name], id)
		}

		names := make([]string, 0, len(nameToIDs))
		for name, ids := range nameToIDs {
			if len(ids) > 1 {
				names = append(names, name)
			}
		}
		sort.Strings(names)

		for _, name := range names {
			groups = append(groups, DuplicateGroup{
				Name:      name,
				ObjectIDs: nameToIDs[name],
				SpaceId:   spaceId,
			})
		}

		return nil
	})

	return groups, err
}
