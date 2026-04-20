package core

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/anyproto/anytype-heart/pb"
	"github.com/anyproto/anytype-heart/pb/service"
	"github.com/anyproto/anytype-heart/pkg/lib/bundle"
	"github.com/anyproto/anytype-heart/pkg/lib/pb/model"
	"github.com/anyproto/anytype-heart/util/pbtypes"
	"github.com/gogo/protobuf/types"
)

var urlRegex = regexp.MustCompile(`https?://\S+`)

// TelegramBotConfig holds configuration for the Telegram bot.
type TelegramBotConfig struct {
	Token         string
	AllowedUserId int64
	SpaceId       string
	CollectionId  string
	SourceRelKey  string // relation key for custom "Source | URL" property
}

// RunTelegramBot starts long-polling and blocks until an error occurs.
func RunTelegramBot(cfg TelegramBotConfig) error {
	bot, err := tgbotapi.NewBotAPI(cfg.Token)
	if err != nil {
		return fmt.Errorf("create bot: %w", err)
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	h := &botHandler{
		bot:    bot,
		cfg:    cfg,
		groups: make(map[string]*mediaGroup),
	}

	for update := range updates {
		if update.Message == nil {
			continue
		}
		h.handleMessage(update.Message)
	}
	return nil
}

// FindRelationKeyByObjectId looks up the relation key string for a relation object by its Anytype object ID.
func FindRelationKeyByObjectId(spaceId, objectId string) (string, error) {
	var key string
	err := GRPCCall(func(ctx context.Context, client service.ClientCommandsClient) error {
		req := &pb.RpcObjectSearchRequest{
			SpaceId: spaceId,
			Filters: []*model.BlockContentDataviewFilter{
				{
					RelationKey: bundle.RelationKeyId.String(),
					Condition:   model.BlockContentDataviewFilter_Equal,
					Value:       pbtypes.String(objectId),
				},
			},
			Keys: []string{
				bundle.RelationKeyId.String(),
				bundle.RelationKeyRelationKey.String(),
			},
		}
		resp, err := client.ObjectSearch(ctx, req)
		if err != nil {
			return fmt.Errorf("search relation: %w", err)
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
		return "", fmt.Errorf("relation object %s not found in space %s", objectId, spaceId)
	}
	return key, nil
}

// AddToCollection adds objectIds to an Anytype collection.
func AddToCollection(collectionId string, objectIds []string) error {
	return GRPCCall(func(ctx context.Context, client service.ClientCommandsClient) error {
		resp, err := client.ObjectCollectionAdd(ctx, &pb.RpcObjectCollectionAddRequest{
			ContextId: collectionId,
			ObjectIds: objectIds,
		})
		if err != nil {
			return fmt.Errorf("collection add: %w", err)
		}
		if resp.Error != nil && resp.Error.Code != pb.RpcObjectCollectionAddResponseError_NULL {
			return fmt.Errorf("collection add error: %s", resp.Error.Description)
		}
		return nil
	})
}

// grpcCallLong is like GRPCCall but with a 2-minute timeout for slow operations like file upload.
func grpcCallLong(fn func(ctx context.Context, client service.ClientCommandsClient) error) error {
	client, err := GetGRPCClient()
	if err != nil {
		return fmt.Errorf("error connecting to gRPC server: %w", err)
	}

	token, _, err := GetStoredSessionToken()
	if err != nil {
		return fmt.Errorf("failed to get stored token: %w", err)
	}

	ctx, cancel := ClientContextWithAuthTimeout(token, 2*time.Minute)
	defer cancel()

	if err := ensureInitialParameters(ctx, client); err != nil {
		return err
	}

	err = fn(ctx, client)
	if err != nil {
		if s, ok := status.FromError(err); ok && s.Code() == codes.Unavailable {
			return fmt.Errorf("anytype is not running. Start it with: anytype serve")
		}
		return err
	}
	return nil
}

type pendingFile struct {
	fileId   string
	fileType string // "photo", "video", "document"
}

type mediaGroup struct {
	mu        sync.Mutex
	sourceURL string
	files     []pendingFile
	timer     *time.Timer
	msgId     int
	chatId    int64
}

type botHandler struct {
	bot    *tgbotapi.BotAPI
	cfg    TelegramBotConfig
	mu     sync.Mutex
	groups map[string]*mediaGroup
}

func (h *botHandler) handleMessage(msg *tgbotapi.Message) {
	if h.cfg.AllowedUserId != 0 && msg.From.ID != h.cfg.AllowedUserId {
		log.Printf("ignored message from unauthorized user %d", msg.From.ID)
		return
	}

	fileId, fileType := extractMedia(msg)
	if fileId == "" {
		log.Printf("message %d has no supported media, skipping", msg.MessageID)
		return
	}

	sourceURL := extractURL(msg.Caption)
	if sourceURL == "" {
		sourceURL = extractURL(msg.Text)
	}
	log.Printf("message %d: type=%s groupId=%q sourceURL=%q", msg.MessageID, fileType, msg.MediaGroupID, sourceURL)

	if msg.MediaGroupID != "" {
		h.addToGroup(msg.MediaGroupID, fileId, fileType, sourceURL, msg.MessageID, msg.Chat.ID)
		return
	}

	objectId, err := h.uploadAndCapture(fileId, fileType, sourceURL)
	if err != nil {
		log.Printf("uploadAndCapture error: %v", err)
		h.reply(msg.Chat.ID, msg.MessageID, "error: "+err.Error())
		return
	}
	link := objectDeepLink(objectId, h.cfg.SpaceId)
	log.Printf("upload ok, objectId=%s, sending link", objectId)
	h.replyLink(msg.Chat.ID, msg.MessageID, link)
}

func (h *botHandler) addToGroup(groupId, fileId, fileType, sourceURL string, msgId int, chatId int64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	g, ok := h.groups[groupId]
	if !ok {
		g = &mediaGroup{chatId: chatId, msgId: msgId, sourceURL: sourceURL}
		h.groups[groupId] = g
	}
	g.files = append(g.files, pendingFile{fileId: fileId, fileType: fileType})
	if sourceURL != "" {
		g.sourceURL = sourceURL
	}

	if g.timer != nil {
		g.timer.Reset(500 * time.Millisecond)
	} else {
		g.timer = time.AfterFunc(500*time.Millisecond, func() {
			h.flushGroup(groupId)
		})
	}
}

func (h *botHandler) flushGroup(groupId string) {
	h.mu.Lock()
	g := h.groups[groupId]
	delete(h.groups, groupId)
	h.mu.Unlock()

	log.Printf("flushing group %s: %d files, sourceURL=%q", groupId, len(g.files), g.sourceURL)

	var objectIds []string
	for _, f := range g.files {
		id, err := h.uploadAndCapture(f.fileId, f.fileType, g.sourceURL)
		if err != nil {
			log.Printf("group %s uploadAndCapture error: %v", groupId, err)
			h.reply(g.chatId, g.msgId, "upload error: "+err.Error())
			return
		}
		log.Printf("group %s uploaded file, objectId=%s", groupId, id)
		objectIds = append(objectIds, id)
	}

	collectionId, err := h.createGroupCollection(g.sourceURL, objectIds)
	if err != nil {
		log.Printf("group %s createGroupCollection error: %v", groupId, err)
		h.reply(g.chatId, g.msgId, "collection error: "+err.Error())
		return
	}

	if err := AddToCollection(h.cfg.CollectionId, []string{collectionId}); err != nil {
		log.Printf("group %s AddToCollection error: %v", groupId, err)
		h.reply(g.chatId, g.msgId, "captures error: "+err.Error())
		return
	}

	h.replyLink(g.chatId, g.msgId, objectDeepLink(collectionId, h.cfg.SpaceId))
}

func (h *botHandler) createGroupCollection(sourceURL string, objectIds []string) (string, error) {
	name := domainFromURL(sourceURL)

	details := &types.Struct{
		Fields: map[string]*types.Value{
			bundle.RelationKeyName.String(): pbtypes.String(name),
		},
	}
	if sourceURL != "" && h.cfg.SourceRelKey != "" {
		details.Fields[h.cfg.SourceRelKey] = pbtypes.String(sourceURL)
	}

	var collectionId string
	err := GRPCCall(func(ctx context.Context, client service.ClientCommandsClient) error {
		resp, err := client.ObjectCreate(ctx, &pb.RpcObjectCreateRequest{
			SpaceId:             h.cfg.SpaceId,
			ObjectTypeUniqueKey: "ot-collection",
			Details:             details,
		})
		if err != nil {
			return fmt.Errorf("create collection: %w", err)
		}
		if resp.Error != nil && resp.Error.Code != pb.RpcObjectCreateResponseError_NULL {
			return fmt.Errorf("create collection error: %s", resp.Error.Description)
		}
		collectionId = resp.ObjectId
		return nil
	})
	if err != nil {
		return "", err
	}

	if err := AddToCollection(collectionId, objectIds); err != nil {
		return "", fmt.Errorf("add objects to group collection: %w", err)
	}
	return collectionId, nil
}

// uploadAndCapture downloads a Telegram file, uploads it to Anytype, sets Source, adds to Captures Markup.
func (h *botHandler) uploadAndCapture(fileId, fileType, sourceURL string) (string, error) {
	tgFile, err := h.bot.GetFile(tgbotapi.FileConfig{FileID: fileId})
	if err != nil {
		return "", fmt.Errorf("get file info: %w", err)
	}

	ext := extFromFileType(fileType)
	if tgFile.FilePath != "" {
		if dot := strings.LastIndex(tgFile.FilePath, "."); dot >= 0 {
			ext = tgFile.FilePath[dot:]
		}
	}

	localPath, err := downloadToTemp(tgFile.Link(h.cfg.Token), ext)
	if err != nil {
		return "", fmt.Errorf("download file: %w", err)
	}
	defer os.Remove(localPath)

	objectId, err := uploadFileToAnytype(localPath, fileType, h.cfg.SpaceId)
	if err != nil {
		return "", fmt.Errorf("upload to anytype: %w", err)
	}

	if sourceURL != "" && h.cfg.SourceRelKey != "" {
		if err := SetObjectTextRelation(objectId, h.cfg.SourceRelKey, sourceURL); err != nil {
			return objectId, fmt.Errorf("set source: %w", err)
		}
	}

	if err := AddToCollection(h.cfg.CollectionId, []string{objectId}); err != nil {
		return objectId, fmt.Errorf("add to captures: %w", err)
	}
	return objectId, nil
}

func (h *botHandler) reply(chatId int64, msgId int, text string) {
	msg := tgbotapi.NewMessage(chatId, text)
	msg.ReplyToMessageID = msgId
	_, _ = h.bot.Send(msg)
}

func (h *botHandler) replyLink(chatId int64, _ int, link string) {
	msg := tgbotapi.NewMessage(chatId, link)
	if _, err := h.bot.Send(msg); err != nil {
		log.Printf("replyLink send error: %v", err)
	}
}

// uploadFileToAnytype uploads a local file to Anytype and returns the object ID.
func uploadFileToAnytype(localPath, fileType, spaceId string) (string, error) {
	mediaType := model.BlockContentFile_Image
	if fileType == "video" {
		mediaType = model.BlockContentFile_Video
	}

	var objectId string
	err := grpcCallLong(func(ctx context.Context, client service.ClientCommandsClient) error {
		resp, err := client.FileUpload(ctx, &pb.RpcFileUploadRequest{
			SpaceId:   spaceId,
			LocalPath: localPath,
			Type:      mediaType,
		})
		if err != nil {
			return fmt.Errorf("file upload: %w", err)
		}
		if resp.Error != nil && resp.Error.Code != pb.RpcFileUploadResponseError_NULL {
			return fmt.Errorf("upload error: %s", resp.Error.Description)
		}
		objectId = resp.ObjectId
		return nil
	})
	return objectId, err
}

func downloadToTemp(rawURL, ext string) (string, error) {
	resp, err := http.Get(rawURL) //nolint:noctx
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	f, err := os.CreateTemp("", "tg-*"+ext)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func extractMedia(msg *tgbotapi.Message) (fileId, fileType string) {
	if msg.Photo != nil && len(msg.Photo) > 0 {
		return msg.Photo[len(msg.Photo)-1].FileID, "photo"
	}
	if msg.Video != nil {
		return msg.Video.FileID, "video"
	}
	if msg.Document != nil {
		return msg.Document.FileID, "document"
	}
	return "", ""
}

func extractURL(text string) string {
	m := urlRegex.FindString(text)
	return strings.TrimRight(m, ".,;)")
}

func domainFromURL(rawURL string) string {
	if rawURL == "" {
		return "Untitled Group"
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	return u.Host
}

func extFromFileType(fileType string) string {
	switch fileType {
	case "video":
		return ".mp4"
	case "photo":
		return ".jpg"
	default:
		return ""
	}
}

func objectDeepLink(objectId, spaceId string) string {
	q := url.Values{}
	q.Set("objectId", objectId)
	q.Set("spaceId", spaceId)
	return "anytype://object?" + q.Encode()
}