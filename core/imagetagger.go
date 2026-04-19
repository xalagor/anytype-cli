package core

import (
	"bufio"
	"context"
	"fmt"
	"io"
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

// DownloadImageSafe downloads an image and renames the local file to use
// objectId as the base name. This avoids problems caused by special characters
// in the original file name on Windows and Linux without touching the object in Anytype.
func DownloadImageSafe(objectId, dir string) (string, error) {
	localPath, err := DownloadImageToDir(objectId, dir)
	if err != nil {
		return "", err
	}
	ext := filepath.Ext(localPath)
	safePath := filepath.Join(dir, objectId+ext)
	if localPath != safePath {
		if renErr := os.Rename(localPath, safePath); renErr == nil {
			return safePath, nil
		}
	}
	return localPath, nil
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

// wdTaggerScript is a persistent Python tagger server using WD EVA02 large v3.
// The model is loaded once at startup; subsequent requests are handled via
// a simple stdin/stdout line protocol.
//
// Usage: python wdtagger.py [general_thresh] [char_thresh]
// Protocol:
//
//	startup  → prints "READY\n" once the model is loaded
//	request  → caller writes "<image_path>\n" to stdin
//	response → script writes "OK:<comma-separated tags>\n" or "ERR:<msg>\n"
const wdTaggerScript = `#!/usr/bin/env python3
"""WD EVA02 large v3 tagger server – loads model once, processes many images.
Usage: python wdtagger.py [general_thresh] [char_thresh]
Protocol (stdin/stdout):
  startup:  prints "READY" once the ONNX model is loaded
  request:  one image path per stdin line
  response: "OK:<comma-separated tags>" or "ERR:<message>" per image
"""
import sys
import csv
import numpy as np
from PIL import Image
from huggingface_hub import hf_hub_download
import onnxruntime as ort

MODEL_REPO = "SmilingWolf/wd-eva02-large-tagger-v3"

def load_model():
    model_path = hf_hub_download(MODEL_REPO, "model.onnx")
    tags_path  = hf_hub_download(MODEL_REPO, "selected_tags.csv")
    sess = ort.InferenceSession(model_path, providers=["CPUExecutionProvider"])
    target_size = int(sess.get_inputs()[0].shape[2])
    tags = []
    with open(tags_path, newline="", encoding="utf-8") as f:
        for row in csv.DictReader(f):
            tags.append(row)
    return sess, tags, target_size

def preprocess(img_path, size):
    img = Image.open(img_path).convert("RGBA")
    bg  = Image.new("RGBA", img.size, (255, 255, 255, 255))
    bg.paste(img, mask=img.split()[3])
    img = bg.convert("RGB")
    w, h = img.size
    m = max(w, h)
    pad = Image.new("RGB", (m, m), (255, 255, 255))
    pad.paste(img, ((m - w) // 2, (m - h) // 2))
    pad = pad.resize((size, size), Image.BICUBIC)
    arr = np.array(pad, dtype=np.float32)[:, :, ::-1]  # RGB -> BGR
    return np.expand_dims(arr, 0)

def tag_image(sess, tags, size, img_path, gen_thresh, char_thresh):
    arr   = preprocess(img_path, size)
    preds = sess.run(None, {sess.get_inputs()[0].name: arr})[0][0]
    result = []
    for i, row in enumerate(tags):
        if i >= len(preds):
            break
        try:
            cat = int(row.get("category", 9))
        except (ValueError, TypeError):
            cat = 9
        if cat == 9:   # skip rating tags
            continue
        thresh = char_thresh if cat == 4 else gen_thresh
        if float(preds[i]) >= thresh:
            result.append(row["name"].replace("_", " "))
    return ", ".join(result)

def main():
    gen_thresh  = float(sys.argv[1]) if len(sys.argv) > 1 else 0.35
    char_thresh = float(sys.argv[2]) if len(sys.argv) > 2 else 0.85

    sess, tags, size = load_model()
    sys.stdout.write("READY\n")
    sys.stdout.flush()

    for line in sys.stdin:
        img_path = line.rstrip("\n")
        if not img_path:
            continue
        try:
            result = tag_image(sess, tags, size, img_path, gen_thresh, char_thresh)
            sys.stdout.write("OK:" + result + "\n")
        except Exception as e:
            sys.stdout.write("ERR:" + str(e).replace("\n", " ") + "\n")
        sys.stdout.flush()

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

// WdTaggerServer manages a long-running Python tagger process.
// The ONNX model is loaded once at startup; subsequent TagImage calls are fast
// because there is no process-launch or model-load overhead.
type WdTaggerServer struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
}

// StartWdTaggerServer launches the Python tagger and blocks until the model is
// fully loaded (signalled by "READY" on stdout). On the first run the model
// must be downloaded from HuggingFace Hub, which may take several minutes;
// download progress is printed to stderr so the user can see it.
func StartWdTaggerServer(python, scriptPath string, genThresh, charThresh float64) (*WdTaggerServer, error) {
	cmd := exec.Command(
		python, scriptPath,
		fmt.Sprintf("%.2f", genThresh),
		fmt.Sprintf("%.2f", charThresh),
	)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("tagger stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("tagger stdout pipe: %w", err)
	}
	// Let Python's stderr (download progress, warnings) flow to the terminal.
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start tagger process: %w", err)
	}

	scanner := bufio.NewScanner(stdoutPipe)
	for scanner.Scan() {
		if scanner.Text() == "READY" {
			return &WdTaggerServer{cmd: cmd, stdin: stdin, stdout: scanner}, nil
		}
	}
	_ = cmd.Wait()
	return nil, fmt.Errorf("tagger process exited before the model was ready")
}

// TagImage sends an image path to the running tagger and returns the
// comma-separated tag string produced by the model.
func (t *WdTaggerServer) TagImage(imagePath string) (string, error) {
	if _, err := fmt.Fprintln(t.stdin, imagePath); err != nil {
		return "", fmt.Errorf("send image path to tagger: %w", err)
	}
	if !t.stdout.Scan() {
		if err := t.stdout.Err(); err != nil {
			return "", fmt.Errorf("read tagger response: %w", err)
		}
		return "", fmt.Errorf("tagger process closed unexpectedly")
	}
	line := t.stdout.Text()
	switch {
	case strings.HasPrefix(line, "OK:"):
		return strings.TrimPrefix(line, "OK:"), nil
	case strings.HasPrefix(line, "ERR:"):
		return "", fmt.Errorf("tagger: %s", strings.TrimPrefix(line, "ERR:"))
	default:
		return "", fmt.Errorf("unexpected tagger output: %s", line)
	}
}

// Close shuts down the tagger process gracefully by closing its stdin.
func (t *WdTaggerServer) Close() error {
	_ = t.stdin.Close()
	return t.cmd.Wait()
}
