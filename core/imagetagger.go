package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/anyproto/anytype-heart/pb"
	"github.com/anyproto/anytype-heart/pb/service"
	"github.com/anyproto/anytype-heart/pkg/lib/bundle"
	"github.com/anyproto/anytype-heart/pkg/lib/pb/model"
	"github.com/anyproto/anytype-heart/util/pbtypes"
)

// ErrImageFormatNotSupported is returned when an image format (e.g. SVG) cannot
// be processed by the ONNX tagger.
var ErrImageFormatNotSupported = errors.New("image format not supported by tagger")

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

// FindImagesWithEmptyRelation returns image objects in the space where the given relation key is empty.
func FindImagesWithEmptyRelation(spaceId, relationKey string) ([]ImageObject, error) {
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

// GetGatewayURL returns the HTTP gateway base URL (e.g. "http://127.0.0.1:47800")
// by opening the workspace for spaceId and reading AccountInfo.GatewayUrl.
func GetGatewayURL(spaceId string) (string, error) {
	var gatewayURL string
	err := GRPCCall(func(ctx context.Context, client service.ClientCommandsClient) error {
		resp, err := client.WorkspaceOpen(ctx, &pb.RpcWorkspaceOpenRequest{SpaceId: spaceId})
		if err != nil {
			return fmt.Errorf("workspace open: %w", err)
		}
		if resp.Error != nil && resp.Error.Code != pb.RpcWorkspaceOpenResponseError_NULL {
			return fmt.Errorf("workspace open error: %s", resp.Error.Description)
		}
		if resp.Info == nil {
			return fmt.Errorf("workspace info is nil")
		}
		gatewayURL = resp.Info.GatewayUrl
		return nil
	})
	return gatewayURL, err
}

// DownloadImageViaGateway fetches an image from the embedded HTTP gateway and
// saves it to dir/<objectId><ext> where ext is derived from the Content-Type
// header. This avoids the gRPC FileDownload path which saves the file using the
// original object name and fails when that name contains Windows-invalid
// characters (?, |, :, ", /, etc.).
//
// Returns ErrImageFormatNotSupported (wrapped) for SVG or other formats that the
// ONNX tagger cannot process. The caller should skip such images gracefully.
func DownloadImageViaGateway(objectId, dir, gatewayURL string) (string, error) {
	url := gatewayURL + "/file/" + objectId
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return "", fmt.Errorf("download image %s: %w", objectId, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download image %s: HTTP %d", objectId, resp.StatusCode)
	}

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "svg") {
		return "", fmt.Errorf("%w: SVG", ErrImageFormatNotSupported)
	}

	ext := contentTypeToExt(ct)
	localPath := filepath.Join(dir, objectId+ext)

	f, err := os.Create(localPath)
	if err != nil {
		return "", fmt.Errorf("create temp file for %s: %w", objectId, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(localPath)
		return "", fmt.Errorf("write image data for %s: %w", objectId, err)
	}
	return localPath, nil
}

// contentTypeToExt maps a Content-Type value to a file extension.
func contentTypeToExt(ct string) string {
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(ct)
	switch ct {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	case "image/tiff":
		return ".tiff"
	case "image/avif":
		return ".avif"
	default:
		return ".jpg"
	}
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

// florenceScript is a persistent Python server that uses Microsoft Florence-2
// to generate natural-language image descriptions.
//
// Usage: python florence2.py [task] [model_id]
//
//	task:     caption | detailed | more-detailed (default: detailed)
//	model_id: HuggingFace model ID (default: microsoft/Florence-2-base)
//
// Protocol (identical to wdTaggerScript):
//
//	startup  → prints "READY\n" once the model is loaded
//	request  → caller writes "<image_path>\n" to stdin
//	response → script writes "OK:<description>\n" or "ERR:<msg>\n"
const florenceScript = `#!/usr/bin/env python3
"""Microsoft Florence-2 captioning server – loads model once, processes many images.
Usage: python florence2.py [task] [model_id]
  task:     caption | detailed | more-detailed  (default: detailed)
  model_id: HuggingFace repo id               (default: microsoft/Florence-2-base)
Protocol (stdin/stdout):
  startup:  prints "READY" once the model is loaded
  request:  one image path per stdin line
  response: "OK:<description>" or "ERR:<message>" per image
"""
import sys
from PIL import Image

TASK_MAP = {
    "caption":      "<CAPTION>",
    "detailed":     "<DETAILED_CAPTION>",
    "more-detailed":"<MORE_DETAILED_CAPTION>",
}

def main():
    task_name = sys.argv[1] if len(sys.argv) > 1 else "detailed"
    model_id  = sys.argv[2] if len(sys.argv) > 2 else "microsoft/Florence-2-base"
    task = TASK_MAP.get(task_name, "<DETAILED_CAPTION>")

    import torch
    from transformers import AutoProcessor, AutoModelForCausalLM
    import transformers.configuration_utils as _cu

    # Florence-2's configuration code (trust_remote_code) accesses
    # self.forced_bos_token_id before super().__init__() sets it.
    # transformers >= 4.46 raises AttributeError in __getattribute__
    # instead of returning None, so we patch it back for this attribute.
    _orig_getattribute = _cu.PretrainedConfig.__getattribute__
    def _safe_getattribute(self, key):
        try:
            return _orig_getattribute(self, key)
        except AttributeError:
            if key == 'forced_bos_token_id':
                return None
            raise
    _cu.PretrainedConfig.__getattribute__ = _safe_getattribute

    # Florence-2's processing_florence2.py accesses tokenizer.additional_special_tokens
    # which in newer transformers raises AttributeError through __getattr__ when the
    # internal _additional_special_tokens list hasn't been initialised yet.
    import transformers.tokenization_utils_base as _tub
    _orig_tok_getattr = _tub.PreTrainedTokenizerBase.__getattr__
    def _safe_tok_getattr(self, key):
        if key == 'additional_special_tokens':
            return getattr(self, '_additional_special_tokens', [])
        return _orig_tok_getattr(self, key)
    _tub.PreTrainedTokenizerBase.__getattr__ = _safe_tok_getattr

    device = "cuda" if torch.cuda.is_available() else "cpu"
    dtype  = torch.float16 if device == "cuda" else torch.float32

    model = AutoModelForCausalLM.from_pretrained(
        model_id,
        torch_dtype=dtype,
        trust_remote_code=True,
        attn_implementation="eager",
    ).to(device)
    model.eval()
    processor = AutoProcessor.from_pretrained(model_id, trust_remote_code=True)

    sys.stdout.write("READY\n")
    sys.stdout.flush()

    for line in sys.stdin:
        img_path = line.rstrip("\n")
        if not img_path:
            continue
        try:
            image = Image.open(img_path).convert("RGB")
            inputs = processor(text=task, images=image, return_tensors="pt").to(device, dtype)
            generated_ids = model.generate(
                input_ids=inputs["input_ids"],
                pixel_values=inputs["pixel_values"],
                max_new_tokens=1024,
                num_beams=3,
            )
            generated_text = processor.batch_decode(generated_ids, skip_special_tokens=False)[0]
            parsed = processor.post_process_generation(
                generated_text,
                task=task,
                image_size=(image.width, image.height),
            )
            description = parsed[task].strip().replace("\n", " ")
            sys.stdout.write("OK:" + description + "\n")
        except Exception as e:
            sys.stdout.write("ERR:" + str(e).replace("\n", " ") + "\n")
        sys.stdout.flush()

if __name__ == "__main__":
    main()
`

// WriteFlorenceScript writes the embedded Florence-2 Python script to a temp file
// and returns the path. The file is reused across calls within a process.
func WriteFlorenceScript() (string, error) {
	scriptPath := filepath.Join(os.TempDir(), "anytype_florence2.py")
	if err := os.WriteFile(scriptPath, []byte(florenceScript), 0o644); err != nil {
		return "", fmt.Errorf("write florence script: %w", err)
	}
	return scriptPath, nil
}

// FlorenceServer manages a long-running Python Florence-2 captioning process.
// The model is loaded once at startup; subsequent DescribeImage calls are fast.
type FlorenceServer struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
}

// pythonMinorVersion returns the minor part of the Python version (e.g. 12 for 3.12).
// Returns 99 on any error so callers assume the newest Python.
func pythonMinorVersion(python string) int {
	out, err := exec.Command(python, "-c", "import sys; print(sys.version_info.minor)").Output()
	if err != nil {
		return 99
	}
	minor, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 99
	}
	return minor
}

// EnsureFlorenceVenv creates (if needed) a Python virtual environment at venvDir
// and installs Florence-2 compatible dependencies with pinned versions.
// Returns the path to the Python interpreter inside the venv.
//
// Florence-2's trust_remote_code scripts were written for transformers ~4.38–4.45;
// newer versions (4.46+) have breaking API changes in several places. Pinning to
// 4.44.2 inside an isolated venv avoids conflicts with the user's system packages.
func EnsureFlorenceVenv(basePython, venvDir string) (string, error) {
	pythonPath := florenceVenvPython(venvDir)

	if _, err := os.Stat(pythonPath); err == nil {
		return pythonPath, nil // venv already ready
	}

	fmt.Fprintf(os.Stderr, "Creating Florence-2 venv at %s …\n", venvDir)
	if out, err := exec.Command(basePython, "-m", "venv", venvDir).CombinedOutput(); err != nil {
		return "", fmt.Errorf("create venv: %w\n%s", err, out)
	}

	pip := florenceVenvPip(venvDir)
	_ = exec.Command(pip, "install", "--quiet", "--upgrade", "pip").Run()

	// On Python ≤3.12 pin transformers to 4.44.2: the last release fully
	// compatible with Florence-2's trust_remote_code scripts, and tokenizers
	// 0.19.x (its dependency) ships pre-built wheels for 3.12 and earlier.
	// On Python 3.13+ tokenizers<0.20 cannot be built (pyo3 0.21 only supports
	// up to 3.12), so we install the latest transformers and rely on the
	// monkey-patches in the Python script to handle API differences.
	minor := pythonMinorVersion(basePython)
	var deps []string
	if minor <= 12 {
		deps = []string{"transformers==4.44.2", "torch", "Pillow", "einops", "timm"}
	} else {
		deps = []string{"torch", "transformers", "Pillow", "einops", "timm"}
	}

	fmt.Fprintf(os.Stderr, "Installing Florence-2 dependencies (%s) – this may take a few minutes…\n",
		strings.Join(deps, ", "))
	installCmd := exec.Command(pip, append([]string{"install"}, deps...)...)
	installCmd.Stdout = os.Stderr
	installCmd.Stderr = os.Stderr
	if err := installCmd.Run(); err != nil {
		os.RemoveAll(venvDir)
		return "", fmt.Errorf("install dependencies: %w", err)
	}

	return pythonPath, nil
}

func florenceVenvPython(venvDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvDir, "Scripts", "python.exe")
	}
	return filepath.Join(venvDir, "bin", "python3")
}

func florenceVenvPip(venvDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvDir, "Scripts", "pip.exe")
	}
	return filepath.Join(venvDir, "bin", "pip3")
}

// StartFlorenceServer launches the Florence-2 process and blocks until the model
// is fully loaded (signalled by "READY" on stdout). On the first run the model
// weights are downloaded from HuggingFace Hub; download progress appears on stderr.
//
// If venvDir is non-empty, EnsureFlorenceVenv is called first to create/reuse
// an isolated venv with compatible dependencies; the venv's Python then replaces
// the basePython for running the script.
func StartFlorenceServer(basePython, scriptPath, task, modelId, venvDir string) (*FlorenceServer, error) {
	python := basePython
	if venvDir != "" {
		var err error
		python, err = EnsureFlorenceVenv(basePython, venvDir)
		if err != nil {
			return nil, fmt.Errorf("setup florence venv: %w", err)
		}
	}

	cmd := exec.Command(python, scriptPath, task, modelId)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("florence stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("florence stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start florence process: %w", err)
	}

	scanner := bufio.NewScanner(stdoutPipe)
	for scanner.Scan() {
		if scanner.Text() == "READY" {
			return &FlorenceServer{cmd: cmd, stdin: stdin, stdout: scanner}, nil
		}
	}
	_ = cmd.Wait()
	return nil, fmt.Errorf("florence process exited before the model was ready")
}

// DescribeImage sends an image path to the running Florence-2 process and returns
// the generated natural-language description.
func (f *FlorenceServer) DescribeImage(imagePath string) (string, error) {
	if _, err := fmt.Fprintln(f.stdin, imagePath); err != nil {
		return "", fmt.Errorf("send image path to florence: %w", err)
	}
	if !f.stdout.Scan() {
		if err := f.stdout.Err(); err != nil {
			return "", fmt.Errorf("read florence response: %w", err)
		}
		return "", fmt.Errorf("florence process closed unexpectedly")
	}
	line := f.stdout.Text()
	switch {
	case strings.HasPrefix(line, "OK:"):
		return strings.TrimPrefix(line, "OK:"), nil
	case strings.HasPrefix(line, "ERR:"):
		return "", fmt.Errorf("florence: %s", strings.TrimPrefix(line, "ERR:"))
	default:
		return "", fmt.Errorf("unexpected florence output: %s", line)
	}
}

// Close shuts down the Florence-2 process gracefully by closing its stdin.
func (f *FlorenceServer) Close() error {
	_ = f.stdin.Close()
	return f.cmd.Wait()
}
