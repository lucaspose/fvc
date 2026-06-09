package dockerimport

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	dockerRegistryHost    = "registry-1.docker.io"
	dockerAuthService     = "registry.docker.io"
	maxDockerLayerBytes   = 10 << 30
	maxDockerRootfsBytes  = 20 << 30
	maxDockerLayerEntries = 1_000_000
	defaultDockerImageMB  = 512
	dockerManifestListMT  = "application/vnd.docker.distribution.manifest.list.v2+json"
	dockerManifestMT      = "application/vnd.docker.distribution.manifest.v2+json"
	ociImageIndexMT       = "application/vnd.oci.image.index.v1+json"
	ociImageManifestMT    = "application/vnd.oci.image.manifest.v1+json"
	dockerConfigMT        = "application/vnd.docker.container.image.v1+json"
	ociConfigMT           = "application/vnd.oci.image.config.v1+json"
	dockerLayerGzipMT     = "application/vnd.docker.image.rootfs.diff.tar.gzip"
	ociLayerGzipMT        = "application/vnd.oci.image.layer.v1.tar+gzip"
	ociLayerGzipNondistMT = "application/vnd.oci.image.layer.nondistributable.v1.tar+gzip"
)

// ProgressFunc receives coarse conversion progress updates.
type ProgressFunc func(stage, status, message string, current, total int64) error

// CommandRunner runs host commands needed to allocate and format ext4 images.
type CommandRunner interface {
	Run(name string, args ...string) error
}

// Options describes a Docker-to-FVC image conversion.
type Options struct {
	SourceRef string
	Target    string
	WorkDir   string
	Runner    CommandRunner
	Progress  ProgressFunc
}

// Result contains the converted rootfs and metadata. Call Cleanup after the
// image has been imported or copied out of ImagePath.
type Result struct {
	ImagePath string
	Metadata  Metadata
	Cleanup   func()
}

// Metadata is portable image metadata produced from a Docker image config.
type Metadata struct {
	Name         string
	Source       string
	CreatedAt    time.Time
	Labels       map[string]string
	Env          []string
	Cmd          []string
	Workdir      string
	ExposedPorts []int32
	History      []History
}

// History records provenance entries produced during conversion.
type History struct {
	Action    string
	Message   string
	CreatedAt time.Time
}

type dockerImageRef struct {
	Repository string
	Reference  string
	Display    string
}

type dockerDescriptor struct {
	MediaType string          `json:"mediaType"`
	Size      int64           `json:"size"`
	Digest    string          `json:"digest"`
	Platform  *dockerPlatform `json:"platform,omitempty"`
}

type dockerPlatform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Variant      string `json:"variant,omitempty"`
}

type dockerIndex struct {
	MediaType string             `json:"mediaType"`
	Manifests []dockerDescriptor `json:"manifests"`
}

type dockerManifest struct {
	SchemaVersion int                `json:"schemaVersion"`
	MediaType     string             `json:"mediaType"`
	Config        dockerDescriptor   `json:"config"`
	Layers        []dockerDescriptor `json:"layers"`
}

type dockerConfig struct {
	Created string `json:"created"`
	Config  struct {
		Env          []string          `json:"Env"`
		Entrypoint   json.RawMessage   `json:"Entrypoint"`
		Cmd          json.RawMessage   `json:"Cmd"`
		WorkingDir   string            `json:"WorkingDir"`
		ExposedPorts map[string]any    `json:"ExposedPorts"`
		Labels       map[string]string `json:"Labels"`
	} `json:"config"`
	ContainerConfig struct {
		Env          []string          `json:"Env"`
		Entrypoint   json.RawMessage   `json:"Entrypoint"`
		Cmd          json.RawMessage   `json:"Cmd"`
		WorkingDir   string            `json:"WorkingDir"`
		ExposedPorts map[string]any    `json:"ExposedPorts"`
		Labels       map[string]string `json:"Labels"`
	} `json:"container_config"`
	History []struct {
		Created    string `json:"created"`
		CreatedBy  string `json:"created_by"`
		EmptyLayer bool   `json:"empty_layer"`
	} `json:"history"`
}

type dockerRegistryClient struct {
	httpClient *http.Client
	tokens     map[string]string
}

// Convert pulls a Docker Hub image, applies its layers, and writes an ext4
// rootfs suitable for Firecracker.
func Convert(ctx context.Context, opts Options) (Result, error) {
	ref, err := parseDockerImageRef(opts.SourceRef)
	if err != nil {
		return Result{}, err
	}
	target := strings.TrimSpace(opts.Target)
	if target == "" {
		return Result{}, fmt.Errorf("target image is required")
	}
	if opts.Runner == nil {
		return Result{}, fmt.Errorf("command runner is required")
	}
	buildDir := strings.TrimSpace(opts.WorkDir)
	if buildDir == "" {
		return Result{}, fmt.Errorf("docker import work directory is required")
	}
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return Result{}, fmt.Errorf("docker import build directory setup failed: %w", err)
	}
	workDir, err := os.MkdirTemp(buildDir, "docker-import-*")
	if err != nil {
		return Result{}, fmt.Errorf("docker import workspace create failed: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(workDir) }
	rootfsDir := filepath.Join(workDir, "rootfs")
	if err := os.MkdirAll(rootfsDir, 0755); err != nil {
		cleanup()
		return Result{}, fmt.Errorf("docker import rootfs create failed: %w", err)
	}

	client := &dockerRegistryClient{httpClient: &http.Client{Timeout: 15 * time.Minute}, tokens: map[string]string{}}
	emitProgress(opts.Progress, "manifest", "running", "Resolving Docker manifest "+ref.Display, 0, 0)
	manifest, configBytes, err := client.fetchDockerManifestAndConfig(ctx, ref)
	if err != nil {
		cleanup()
		return Result{}, err
	}
	metadata, err := dockerMetadataFromConfig(configBytes, target, ref.Display)
	if err != nil {
		cleanup()
		return Result{}, err
	}
	emitProgress(opts.Progress, "manifest", "complete", "Docker manifest ready", 0, 0)

	for i, layer := range manifest.Layers {
		if !isSupportedDockerLayer(layer.MediaType) {
			cleanup()
			return Result{}, fmt.Errorf("unsupported Docker layer media type %q", layer.MediaType)
		}
		message := fmt.Sprintf("Downloading layer %d/%d", i+1, len(manifest.Layers))
		emitProgress(opts.Progress, "layer", "running", message, int64(i), int64(len(manifest.Layers)))
		if err := client.downloadAndApplyLayer(ctx, ref, layer, rootfsDir); err != nil {
			cleanup()
			return Result{}, err
		}
	}
	emitProgress(opts.Progress, "layer", "complete", "Docker layers applied", int64(len(manifest.Layers)), int64(len(manifest.Layers)))

	imagePath := filepath.Join(workDir, "image.ext4")
	emitProgress(opts.Progress, "ext4", "running", "Creating Firecracker rootfs ext4", 0, 0)
	sizeMB, err := rootfsImageSizeMB(rootfsDir)
	if err != nil {
		cleanup()
		return Result{}, err
	}
	if err := createExt4FromRootfs(opts.Runner, rootfsDir, imagePath, sizeMB); err != nil {
		cleanup()
		return Result{}, err
	}
	emitProgress(opts.Progress, "ext4", "complete", "Firecracker rootfs created", 0, 0)

	return Result{ImagePath: imagePath, Metadata: metadata, Cleanup: cleanup}, nil
}

func emitProgress(progress ProgressFunc, stage, status, message string, current, total int64) error {
	if progress == nil {
		return nil
	}
	return progress(stage, status, message, current, total)
}

func parseDockerImageRef(value string) (dockerImageRef, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return dockerImageRef{}, errors.New("Docker image is required")
	}
	if strings.Contains(value, "://") || strings.HasPrefix(value, "/") {
		return dockerImageRef{}, fmt.Errorf("Docker image %q must be a Docker Hub reference like ubuntu:24.04 or lucas/app:latest", value)
	}
	name, reference := splitDockerReference(value)
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return dockerImageRef{}, fmt.Errorf("Docker image %q contains an invalid path segment", value)
		}
	}
	repository := name
	if !strings.Contains(repository, "/") {
		repository = "library/" + repository
	}
	return dockerImageRef{Repository: repository, Reference: reference, Display: name + ":" + reference}, nil
}

func splitDockerReference(value string) (string, string) {
	lastSlash := strings.LastIndex(value, "/")
	lastColon := strings.LastIndex(value, ":")
	if lastColon > lastSlash {
		return value[:lastColon], value[lastColon+1:]
	}
	return value, "latest"
}

func (c *dockerRegistryClient) fetchDockerManifestAndConfig(ctx context.Context, ref dockerImageRef) (dockerManifest, []byte, error) {
	body, mediaType, err := c.getRegistryBlob(ctx, ref.Repository, "manifests/"+ref.Reference, acceptManifestHeader())
	if err != nil {
		return dockerManifest{}, nil, err
	}
	if mediaType == "" {
		mediaType = sniffManifestMediaType(body)
	}
	if mediaType == dockerManifestListMT || mediaType == ociImageIndexMT {
		var index dockerIndex
		if err := json.Unmarshal(body, &index); err != nil {
			return dockerManifest{}, nil, fmt.Errorf("manifest list parse failed: %w", err)
		}
		descriptor, err := selectPlatformManifest(index.Manifests)
		if err != nil {
			return dockerManifest{}, nil, err
		}
		body, mediaType, err = c.getRegistryBlob(ctx, ref.Repository, "manifests/"+descriptor.Digest, acceptManifestHeader())
		if err != nil {
			return dockerManifest{}, nil, err
		}
	}
	if mediaType != dockerManifestMT && mediaType != ociImageManifestMT && mediaType != "" {
		return dockerManifest{}, nil, fmt.Errorf("unsupported Docker manifest media type %q", mediaType)
	}
	var manifest dockerManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return dockerManifest{}, nil, fmt.Errorf("manifest parse failed: %w", err)
	}
	if manifest.SchemaVersion != 2 {
		return dockerManifest{}, nil, fmt.Errorf("unsupported Docker manifest schema version %d", manifest.SchemaVersion)
	}
	if manifest.Config.Digest == "" || len(manifest.Layers) == 0 {
		return dockerManifest{}, nil, fmt.Errorf("Docker manifest is missing config or layers")
	}
	if manifest.Config.MediaType != "" && manifest.Config.MediaType != dockerConfigMT && manifest.Config.MediaType != ociConfigMT {
		return dockerManifest{}, nil, fmt.Errorf("unsupported Docker config media type %q", manifest.Config.MediaType)
	}
	configBytes, _, err := c.getRegistryBlob(ctx, ref.Repository, "blobs/"+manifest.Config.Digest, "")
	if err != nil {
		return dockerManifest{}, nil, err
	}
	if err := verifyDigestBytes(configBytes, manifest.Config.Digest); err != nil {
		return dockerManifest{}, nil, fmt.Errorf("config digest verification failed: %w", err)
	}
	return manifest, configBytes, nil
}

func acceptManifestHeader() string {
	return strings.Join([]string{dockerManifestListMT, ociImageIndexMT, dockerManifestMT, ociImageManifestMT}, ", ")
}

func sniffManifestMediaType(data []byte) string {
	var raw struct {
		Manifests []json.RawMessage `json:"manifests"`
		Layers    []json.RawMessage `json:"layers"`
	}
	if json.Unmarshal(data, &raw) == nil && len(raw.Manifests) > 0 {
		return dockerManifestListMT
	}
	return dockerManifestMT
}

func selectPlatformManifest(manifests []dockerDescriptor) (dockerDescriptor, error) {
	wantArch := runtime.GOARCH
	for _, descriptor := range manifests {
		if descriptor.Platform == nil {
			continue
		}
		if descriptor.Platform.OS == "linux" && descriptor.Platform.Architecture == wantArch {
			return descriptor, nil
		}
	}
	for _, descriptor := range manifests {
		if descriptor.Platform != nil && descriptor.Platform.OS == "linux" && descriptor.Platform.Architecture == "amd64" {
			return descriptor, nil
		}
	}
	return dockerDescriptor{}, fmt.Errorf("no linux/%s or linux/amd64 manifest found", wantArch)
}

func (c *dockerRegistryClient) getRegistryBlob(ctx context.Context, repository, path, accept string) ([]byte, string, error) {
	resp, err := c.doRegistryGet(ctx, repository, path, accept)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDockerLayerBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("registry response read failed: %w", err)
	}
	if int64(len(data)) > maxDockerLayerBytes {
		return nil, "", fmt.Errorf("registry response exceeds maximum size")
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mediaType == "" {
		mediaType = resp.Header.Get("Docker-Content-Digest")
	}
	return data, mediaType, nil
}

func (c *dockerRegistryClient) doRegistryGet(ctx context.Context, repository, path, accept string) (*http.Response, error) {
	endpoint := "https://" + dockerRegistryHost + "/v2/" + repository + "/" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if token := c.tokens[repository]; token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registry request failed: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		auth := resp.Header.Get("WWW-Authenticate")
		_ = resp.Body.Close()
		token, err := c.fetchBearerToken(ctx, auth, repository)
		if err != nil {
			return nil, err
		}
		c.tokens[repository] = token
		return c.doRegistryGet(ctx, repository, path, accept)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("registry returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if resp.ContentLength > maxDockerLayerBytes {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("registry response exceeds maximum size")
	}
	return resp, nil
}

func (c *dockerRegistryClient) fetchBearerToken(ctx context.Context, header, repository string) (string, error) {
	scheme, params := parseBearerChallenge(header)
	if !strings.EqualFold(scheme, "Bearer") {
		return "", fmt.Errorf("Docker Hub requires an unsupported auth challenge: %s", header)
	}
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("Docker Hub auth challenge missing realm")
	}
	tokenURL, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("Docker Hub auth realm parse failed: %w", err)
	}
	query := tokenURL.Query()
	service := params["service"]
	if service == "" {
		service = dockerAuthService
	}
	query.Set("service", service)
	scope := params["scope"]
	if scope == "" {
		scope = "repository:" + repository + ":pull"
	}
	query.Set("scope", scope)
	tokenURL.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("Docker Hub token request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("Docker Hub token request returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var parsed struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("Docker Hub token parse failed: %w", err)
	}
	if parsed.Token != "" {
		return parsed.Token, nil
	}
	if parsed.AccessToken != "" {
		return parsed.AccessToken, nil
	}
	return "", fmt.Errorf("Docker Hub token response did not include a token")
}

func parseBearerChallenge(header string) (string, map[string]string) {
	fields := strings.SplitN(header, " ", 2)
	if len(fields) != 2 {
		return strings.TrimSpace(header), map[string]string{}
	}
	params := map[string]string{}
	for _, part := range strings.Split(fields[1], ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		value = strings.Trim(value, `"`)
		params[strings.ToLower(key)] = value
	}
	return fields[0], params
}

func isSupportedDockerLayer(mediaType string) bool {
	return mediaType == dockerLayerGzipMT || mediaType == ociLayerGzipMT || mediaType == ociLayerGzipNondistMT || mediaType == ""
}

func (c *dockerRegistryClient) downloadAndApplyLayer(ctx context.Context, ref dockerImageRef, layer dockerDescriptor, rootfsDir string) error {
	resp, err := c.doRegistryGet(ctx, ref.Repository, "blobs/"+layer.Digest, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	digestReader, err := newDigestVerifier(resp.Body, layer.Digest, maxDockerLayerBytes)
	if err != nil {
		return err
	}
	gzipReader, err := gzip.NewReader(digestReader)
	if err != nil {
		return fmt.Errorf("layer gzip open failed: %w", err)
	}
	defer gzipReader.Close()
	if err := applyTarLayer(rootfsDir, gzipReader); err != nil {
		return err
	}
	if err := digestReader.Verify(); err != nil {
		return fmt.Errorf("layer digest verification failed: %w", err)
	}
	return nil
}

func verifyDigestBytes(data []byte, expected string) error {
	algo, value, ok := strings.Cut(expected, ":")
	if !ok || algo != "sha256" || len(value) != 64 {
		return fmt.Errorf("unsupported digest %q", expected)
	}
	sum := sha256.Sum256(data)
	actual := hex.EncodeToString(sum[:])
	if actual != value {
		return fmt.Errorf("expected sha256:%s, got sha256:%s", value, actual)
	}
	return nil
}

type digestVerifier struct {
	reader io.Reader
	hash   hashWriter
	expect string
	limit  int64
	read   int64
}

type hashWriter interface {
	io.Writer
	Sum([]byte) []byte
}

func newDigestVerifier(reader io.Reader, expected string, limit int64) (*digestVerifier, error) {
	algo, value, ok := strings.Cut(expected, ":")
	if !ok || algo != "sha256" || len(value) != 64 {
		return nil, fmt.Errorf("unsupported digest %q", expected)
	}
	return &digestVerifier{reader: reader, hash: sha256.New(), expect: value, limit: limit}, nil
}

func (d *digestVerifier) Read(p []byte) (int, error) {
	if d.read > d.limit {
		return 0, fmt.Errorf("registry response exceeds maximum size")
	}
	n, err := d.reader.Read(p)
	if n > 0 {
		d.read += int64(n)
		if d.read > d.limit {
			return n, fmt.Errorf("registry response exceeds maximum size")
		}
		_, _ = d.hash.Write(p[:n])
	}
	return n, err
}

func (d *digestVerifier) Verify() error {
	actual := hex.EncodeToString(d.hash.Sum(nil))
	if actual != d.expect {
		return fmt.Errorf("expected sha256:%s, got sha256:%s", d.expect, actual)
	}
	return nil
}

func applyTarLayer(rootfsDir string, reader io.Reader) error {
	tarReader := tar.NewReader(reader)
	var totalBytes int64
	var entries int64
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("layer tar read failed: %w", err)
		}
		entries++
		if entries > maxDockerLayerEntries {
			return fmt.Errorf("Docker layer has too many entries")
		}
		if header.Size < 0 {
			return fmt.Errorf("Docker layer entry has negative size: %s", header.Name)
		}
		totalBytes += header.Size
		if totalBytes > maxDockerRootfsBytes {
			return fmt.Errorf("Docker layer expands beyond maximum rootfs size")
		}
		if err := applyTarEntry(rootfsDir, header, tarReader); err != nil {
			return err
		}
	}
}

func applyTarEntry(rootfsDir string, header *tar.Header, reader io.Reader) error {
	name := cleanLayerPath(header.Name)
	if name == "" {
		return nil
	}
	base := filepath.Base(name)
	dir := filepath.Dir(name)
	if strings.HasPrefix(base, ".wh.") {
		return applyWhiteout(rootfsDir, dir, base)
	}
	targetPath, err := secureRootfsPath(rootfsDir, name)
	if err != nil {
		return err
	}
	if err := ensureNoSymlinkAncestors(rootfsDir, filepath.Dir(targetPath)); err != nil {
		return err
	}
	mode := os.FileMode(header.Mode) & 07777
	if mode == 0 {
		mode = 0644
	}
	isSymlink := false
	switch header.Typeflag {
	case tar.TypeDir:
		if err := ensureDirNoSymlink(rootfsDir, targetPath, mode); err != nil {
			return fmt.Errorf("directory create failed for %s: %w", name, err)
		}
		return os.Chmod(targetPath, mode)
	case tar.TypeReg, tar.TypeRegA:
		if header.Size > maxDockerLayerBytes {
			return fmt.Errorf("file %s exceeds maximum Docker layer file size", name)
		}
		if err := ensureDirNoSymlink(rootfsDir, filepath.Dir(targetPath), 0755); err != nil {
			return fmt.Errorf("file parent create failed for %s: %w", name, err)
		}
		file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, mode)
		if err != nil {
			return fmt.Errorf("file create failed for %s: %w", name, err)
		}
		written, copyErr := io.Copy(file, reader)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("file extract failed for %s: %w", name, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("file close failed for %s: %w", name, closeErr)
		}
		if written != header.Size {
			return fmt.Errorf("file %s extracted size mismatch: expected %d, wrote %d", name, header.Size, written)
		}
		if err := os.Chmod(targetPath, mode); err != nil {
			return fmt.Errorf("file chmod failed for %s: %w", name, err)
		}
	case tar.TypeSymlink:
		if err := ensureDirNoSymlink(rootfsDir, filepath.Dir(targetPath), 0755); err != nil {
			return fmt.Errorf("symlink parent create failed for %s: %w", name, err)
		}
		_ = os.Remove(targetPath)
		if err := os.Symlink(header.Linkname, targetPath); err != nil {
			return fmt.Errorf("symlink create failed for %s: %w", name, err)
		}
		isSymlink = true
	case tar.TypeLink:
		linkName := cleanLayerPath(header.Linkname)
		if linkName == "" {
			return fmt.Errorf("hardlink target is invalid for %s", name)
		}
		linkTarget, err := secureRootfsPath(rootfsDir, linkName)
		if err != nil {
			return err
		}
		if err := ensureNoSymlinkAncestors(rootfsDir, filepath.Dir(targetPath)); err != nil {
			return err
		}
		if err := ensureDirNoSymlink(rootfsDir, filepath.Dir(targetPath), 0755); err != nil {
			return fmt.Errorf("hardlink parent create failed for %s: %w", name, err)
		}
		_ = os.Remove(targetPath)
		if err := os.Link(linkTarget, targetPath); err != nil {
			return fmt.Errorf("hardlink create failed for %s: %w", name, err)
		}
	case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
		return nil
	default:
		return fmt.Errorf("unsupported tar entry type %d for %s", header.Typeflag, name)
	}
	if !isSymlink && !header.ModTime.IsZero() {
		_ = os.Chtimes(targetPath, header.ModTime, header.ModTime)
	}
	return nil
}

func cleanLayerPath(name string) string {
	name = strings.TrimSpace(filepath.ToSlash(name))
	name = strings.TrimPrefix(name, "/")
	clean := filepath.Clean(name)
	if clean == "." || clean == "" || strings.HasPrefix(clean, "../") || clean == ".." {
		return ""
	}
	return filepath.FromSlash(clean)
}

func applyWhiteout(rootfsDir, dir, name string) error {
	parent, err := secureRootfsPath(rootfsDir, dir)
	if err != nil {
		return err
	}
	if name == ".wh..wh..opq" {
		entries, err := os.ReadDir(parent)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("opaque whiteout read failed: %w", err)
		}
		for _, entry := range entries {
			if err := os.RemoveAll(filepath.Join(parent, entry.Name())); err != nil {
				return fmt.Errorf("opaque whiteout remove failed: %w", err)
			}
		}
		return nil
	}
	target, err := secureRootfsPath(parent, strings.TrimPrefix(name, ".wh."))
	if err != nil {
		return err
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("whiteout remove failed for %s: %w", name, err)
	}
	return nil
}

func secureRootfsPath(root, rel string) (string, error) {
	root = filepath.Clean(root)
	target := filepath.Clean(filepath.Join(root, rel))
	if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
		return "", fmt.Errorf("layer path escapes rootfs: %s", rel)
	}
	return target, nil
}

func ensureNoSymlinkAncestors(root, targetDir string) error {
	root = filepath.Clean(root)
	targetDir = filepath.Clean(targetDir)
	rel, err := filepath.Rel(root, targetDir)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("path stat failed for %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing layer path through symlink: %s", current)
		}
	}
	return nil
}

func ensureDirNoSymlink(root, dir string, mode os.FileMode) error {
	root = filepath.Clean(root)
	dir = filepath.Clean(dir)
	if dir != root && !strings.HasPrefix(dir, root+string(filepath.Separator)) {
		return fmt.Errorf("directory escapes rootfs: %s", dir)
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return err
	}
	current := root
	if rel == "." {
		return nil
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("refusing layer path through symlink: %s", current)
			}
			if !info.IsDir() {
				return fmt.Errorf("layer path component is not a directory: %s", current)
			}
			continue
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("path stat failed for %s: %w", current, err)
		}
		if err := os.Mkdir(current, mode); err != nil && !os.IsExist(err) {
			return err
		}
	}
	return nil
}

func rootfsImageSizeMB(rootfsDir string) (int64, error) {
	var total int64
	if err := filepath.WalkDir(rootfsDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
			if total > maxDockerRootfsBytes {
				return fmt.Errorf("rootfs exceeds maximum size")
			}
		}
		return nil
	}); err != nil {
		return 0, fmt.Errorf("rootfs size scan failed: %w", err)
	}
	mb := total/(1024*1024) + total/(4*1024*1024) + 256
	if mb < defaultDockerImageMB {
		mb = defaultDockerImageMB
	}
	return mb, nil
}

func createExt4FromRootfs(runner CommandRunner, rootfsDir, imagePath string, sizeMB int64) error {
	if err := runner.Run("truncate", "-s", strconv.FormatInt(sizeMB, 10)+"M", imagePath); err != nil {
		return fmt.Errorf("rootfs image allocation failed: %w", err)
	}
	if err := runner.Run("mkfs.ext4", "-q", "-F", "-d", rootfsDir, imagePath); err != nil {
		return fmt.Errorf("rootfs ext4 create failed: %w", err)
	}
	return nil
}

func dockerMetadataFromConfig(data []byte, target, source string) (Metadata, error) {
	var config dockerConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return Metadata{}, fmt.Errorf("Docker config parse failed: %w", err)
	}
	createdAt := time.Now()
	if config.Created != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, config.Created); err == nil {
			createdAt = parsed
		}
	}
	env := config.Config.Env
	if len(env) == 0 {
		env = config.ContainerConfig.Env
	}
	workdir := strings.TrimSpace(config.Config.WorkingDir)
	if workdir == "" {
		workdir = strings.TrimSpace(config.ContainerConfig.WorkingDir)
	}
	cmd := appendDockerCommand(parseDockerCommand(config.Config.Entrypoint), parseDockerCommand(config.Config.Cmd)...)
	if len(cmd) == 0 {
		cmd = appendDockerCommand(parseDockerCommand(config.ContainerConfig.Entrypoint), parseDockerCommand(config.ContainerConfig.Cmd)...)
	}
	labels := config.Config.Labels
	if len(labels) == 0 {
		labels = config.ContainerConfig.Labels
	}
	exposed := dockerExposedPorts(config.Config.ExposedPorts)
	if len(exposed) == 0 {
		exposed = dockerExposedPorts(config.ContainerConfig.ExposedPorts)
	}
	history := []History{{
		Action:    "docker-pull",
		Message:   "converted Docker image " + source,
		CreatedAt: time.Now(),
	}}
	for _, entry := range config.History {
		if strings.TrimSpace(entry.CreatedBy) == "" {
			continue
		}
		created := createdAt
		if entry.Created != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, entry.Created); err == nil {
				created = parsed
			}
		}
		history = append(history, History{Action: "docker-layer", Message: entry.CreatedBy, CreatedAt: created})
	}
	return Metadata{
		Name:         target,
		Source:       "docker:" + source,
		CreatedAt:    createdAt,
		Labels:       cloneLabels(labels),
		Env:          append([]string(nil), env...),
		Cmd:          cmd,
		Workdir:      workdir,
		ExposedPorts: exposed,
		History:      history,
	}, nil
}

func cloneLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels))
	for key, value := range labels {
		out[key] = value
	}
	return out
}

func parseDockerCommand(raw json.RawMessage) []string {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil && strings.TrimSpace(single) != "" {
		return []string{"/bin/sh", "-c", single}
	}
	return nil
}

func appendDockerCommand(base []string, extra ...string) []string {
	out := append([]string(nil), base...)
	out = append(out, extra...)
	return out
}

func dockerExposedPorts(values map[string]any) []int32 {
	ports := make([]int32, 0, len(values))
	for value := range values {
		portText, protoText, ok := strings.Cut(value, "/")
		if !ok || protoText != "tcp" {
			continue
		}
		port, err := strconv.Atoi(portText)
		if err == nil && port >= 1 && port <= 65535 {
			ports = append(ports, int32(port))
		}
	}
	return ports
}
