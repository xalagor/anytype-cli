package telegram

import (
	"fmt"
	"os"
	"strconv"

	"github.com/anyproto/anytype-cli/core"
	"github.com/spf13/cobra"
)

const (
	defaultSpaceId      = "bafyreigexrsmsgmmvsihpmlmwpatwch7qtd44qm2i27skm4jrc4pzdbnim.3mddx2txwkmrn"
	defaultCollectionId = "bafyreihd7ufgfbakwcwluzioysrt5dw6nfueels5uilvdpmrjtwxahk3mi"
	sourceRelObjectId   = "bafyreiciy7gpgdnsb2s3qdgvanvsksxkhbczlzf63vnhazzk2sqcyco2xu"
)

func NewTelegramCmd() *cobra.Command {
	var (
		token        string
		spaceId      string
		collectionId string
		userIdStr    string
	)

	cmd := &cobra.Command{
		Use:   "telegram",
		Short: "Start Telegram bot for media capture to Anytype",
		RunE: func(cmd *cobra.Command, args []string) error {
			if token == "" {
				token = os.Getenv("TELEGRAM_TOKEN")
			}
			if token == "" {
				return fmt.Errorf("bot token required: set --token or TELEGRAM_TOKEN env var")
			}

			if spaceId == "" {
				spaceId = os.Getenv("ANYTYPE_SPACE_ID")
			}
			if spaceId == "" {
				spaceId = defaultSpaceId
			}

			if collectionId == "" {
				collectionId = os.Getenv("ANYTYPE_COLLECTION_ID")
			}
			if collectionId == "" {
				collectionId = defaultCollectionId
			}

			if userIdStr == "" {
				userIdStr = os.Getenv("TELEGRAM_USER_ID")
			}
			if userIdStr == "" {
				return fmt.Errorf("allowed user ID required: set --user or TELEGRAM_USER_ID env var")
			}
			allowedUserId, err := strconv.ParseInt(userIdStr, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid user ID %q: %w", userIdStr, err)
			}

			fmt.Println("Resolving Source relation key...")
			sourceRelKey, err := core.FindRelationKeyByObjectId(spaceId, sourceRelObjectId)
			if err != nil {
				return fmt.Errorf("resolve source relation: %w", err)
			}
			fmt.Printf("Source relation key: %s\n", sourceRelKey)
			fmt.Printf("Starting Telegram bot (space: %s)...\n", spaceId)

			return core.RunTelegramBot(core.TelegramBotConfig{
				Token:         token,
				AllowedUserId: allowedUserId,
				SpaceId:       spaceId,
				CollectionId:  collectionId,
				SourceRelKey:  sourceRelKey,
			})
		},
	}

	cmd.Flags().StringVar(&token, "token", "", "Telegram bot token (or TELEGRAM_TOKEN env)")
	cmd.Flags().StringVar(&spaceId, "space", "", "Anytype space ID (or ANYTYPE_SPACE_ID env)")
	cmd.Flags().StringVar(&collectionId, "collection", "", "Captures Markup collection ID (or ANYTYPE_COLLECTION_ID env)")
	cmd.Flags().StringVar(&userIdStr, "user", "", "Allowed Telegram user ID (or TELEGRAM_USER_ID env)")

	return cmd
}
