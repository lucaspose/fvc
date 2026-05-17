package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/lucaspose/fvc/internal"
)

type FvcfileConfig struct {
	Image FvcfileImage  `toml:"image"`
	Copy  []FvcfileCopy `toml:"copy"`
}

type FvcfileImage struct {
	From string `toml:"from"`
	Tag  string `toml:"tag"`
}

type FvcfileCopy struct {
	Src  string `toml:"src"`
	Dest string `toml:"dest"`
}

type BuildPlan struct {
	ContextPath string
	BaseImage   string
	Tag         string
	Copies      []BuildCopy
}

type BuildCopy struct {
	SourcePath string
	DestPath   string
}

func (s *ImageStore) BuildImage(plan BuildPlan, runner CommandRunner) (string, error) {
	if runner == nil {
		runner = realCommandRunner{}
	}
	if err := internal.ValidateImageRef(plan.BaseImage); err != nil {
		return "", err
	}
	if err := internal.ValidateImageRef(plan.Tag); err != nil {
		return "", err
	}

	basePath, err := s.PullImageIfNeeded(plan.BaseImage)
	if err != nil {
		return "", err
	}
	buildDir := filepath.Join(s.baseDir, "build")
	if err := os.MkdirAll(s.cacheDir, 0755); err != nil {
		return "", fmt.Errorf("cache directory setup failed: %w", err)
	}
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return "", fmt.Errorf("build directory setup failed: %w", err)
	}

	tmpImagePath, cleanup, err := s.createTemporaryBuildImage(buildDir, plan.Tag)
	if err != nil {
		return "", err
	}
	defer cleanup()

	if _, err := copyFileAtomic(basePath, tmpImagePath); err != nil {
		return "", err
	}
	if err := s.applyBuildPlan(tmpImagePath, buildDir, plan, runner); err != nil {
		return "", err
	}

	finalPath := s.cachedImagePath(plan.Tag)
	if err := os.Rename(tmpImagePath, finalPath); err != nil {
		return "", fmt.Errorf("image publish failed: %w", err)
	}
	if err := s.WriteBuildMetadata(plan); err != nil {
		return "", err
	}
	return finalPath, nil
}

func (s *ImageStore) createTemporaryBuildImage(buildDir, tag string) (string, func(), error) {
	tmpImage, err := os.CreateTemp(buildDir, "."+filepath.Base(s.cachedImagePath(tag))+".tmp-*")
	if err != nil {
		return "", nil, fmt.Errorf("temporary build image create failed: %w", err)
	}
	tmpImagePath := tmpImage.Name()
	if err := tmpImage.Close(); err != nil {
		_ = os.Remove(tmpImagePath)
		return "", nil, fmt.Errorf("temporary build image close failed: %w", err)
	}
	cleanup := func() {
		_ = os.Remove(tmpImagePath)
	}
	return tmpImagePath, cleanup, nil
}

func (s *ImageStore) applyBuildPlan(imagePath, buildDir string, plan BuildPlan, runner CommandRunner) error {
	mountDir, err := os.MkdirTemp(buildDir, "mnt-*")
	if err != nil {
		return fmt.Errorf("build mount directory create failed: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(mountDir)
	}()

	if err := runner.Run("mount", "-o", "loop", imagePath, mountDir); err != nil {
		return fmt.Errorf("rootfs mount failed: %w", err)
	}
	mounted := true
	defer func() {
		if mounted {
			_ = runner.Run("umount", mountDir)
		}
	}()

	for _, copySpec := range plan.Copies {
		if err := copyIntoRootfs(copySpec.SourcePath, mountDir, copySpec.DestPath); err != nil {
			return err
		}
	}

	if err := runner.Run("umount", mountDir); err != nil {
		return fmt.Errorf("rootfs unmount failed: %w", err)
	}
	mounted = false
	return nil
}

func LoadBuildPlan(contextPath, tagOverride string) (BuildPlan, error) {
	if strings.TrimSpace(contextPath) == "" {
		contextPath = "."
	}
	absContext, err := filepath.Abs(contextPath)
	if err != nil {
		return BuildPlan{}, fmt.Errorf("build context resolve failed: %w", err)
	}
	info, err := os.Stat(absContext)
	if err != nil {
		return BuildPlan{}, fmt.Errorf("build context read failed: %w", err)
	}
	if !info.IsDir() {
		return BuildPlan{}, fmt.Errorf("build context must be a directory")
	}

	var config FvcfileConfig
	fvcfilePath := filepath.Join(absContext, "Fvcfile")
	if _, err := toml.DecodeFile(fvcfilePath, &config); err != nil {
		return BuildPlan{}, fmt.Errorf("Fvcfile parse failed: %w", err)
	}

	baseImage := strings.TrimSpace(config.Image.From)
	if err := internal.ValidateImageRef(baseImage); err != nil {
		return BuildPlan{}, fmt.Errorf("image.from: %w", err)
	}

	tag := strings.TrimSpace(tagOverride)
	if tag == "" {
		tag = strings.TrimSpace(config.Image.Tag)
	}
	if err := internal.ValidateImageRef(tag); err != nil {
		return BuildPlan{}, fmt.Errorf("image tag: %w", err)
	}

	copies := make([]BuildCopy, 0, len(config.Copy))
	for _, copySpec := range config.Copy {
		sourcePath, err := resolveBuildSource(absContext, copySpec.Src)
		if err != nil {
			return BuildPlan{}, err
		}
		destPath, err := resolveBuildDest(copySpec.Dest)
		if err != nil {
			return BuildPlan{}, err
		}
		copies = append(copies, BuildCopy{SourcePath: sourcePath, DestPath: destPath})
	}

	return BuildPlan{
		ContextPath: absContext,
		BaseImage:   baseImage,
		Tag:         tag,
		Copies:      copies,
	}, nil
}

func resolveBuildSource(contextPath, src string) (string, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return "", fmt.Errorf("copy.src is required")
	}
	if filepath.IsAbs(src) {
		return "", fmt.Errorf("copy.src must be relative to the build context")
	}
	clean := filepath.Clean(src)
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("copy.src escapes the build context: %s", src)
	}
	sourcePath := filepath.Join(contextPath, clean)
	realContext, err := filepath.EvalSymlinks(contextPath)
	if err != nil {
		return "", fmt.Errorf("build context symlink resolve failed: %w", err)
	}
	realSource, err := filepath.EvalSymlinks(sourcePath)
	if err != nil {
		return "", fmt.Errorf("copy source unavailable %s: %w", src, err)
	}
	if !pathWithin(realContext, realSource) {
		return "", fmt.Errorf("copy.src escapes the build context: %s", src)
	}
	if _, err := os.Stat(sourcePath); err != nil {
		return "", fmt.Errorf("copy source unavailable %s: %w", src, err)
	}
	return sourcePath, nil
}

func resolveBuildDest(dest string) (string, error) {
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return "", fmt.Errorf("copy.dest is required")
	}
	if !filepath.IsAbs(dest) {
		return "", fmt.Errorf("copy.dest must be an absolute guest path")
	}
	clean := filepath.Clean(dest)
	for _, segment := range strings.Split(clean, string(filepath.Separator)) {
		if segment == ".." {
			return "", fmt.Errorf("copy.dest contains an invalid path segment")
		}
	}
	if clean == string(filepath.Separator) {
		return "", fmt.Errorf("copy.dest cannot be the guest root")
	}
	return clean, nil
}

func copyIntoRootfs(sourcePath, mountDir, guestDest string) error {
	targetPath, err := rootfsPath(mountDir, guestDest)
	if err != nil {
		return err
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("copy source stat failed: %w", err)
	}
	if info.IsDir() {
		return copyDir(sourcePath, targetPath)
	}
	return copyRegularFile(sourcePath, targetPath, info.Mode())
}

func copyDir(sourceDir, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("copy directory create failed: %w", err)
	}
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return fmt.Errorf("copy directory read failed: %w", err)
	}
	for _, entry := range entries {
		sourcePath := filepath.Join(sourceDir, entry.Name())
		targetPath := filepath.Join(targetDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("copy entry stat failed: %w", err)
		}
		if info.IsDir() {
			if err := copyDir(sourcePath, targetPath); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if err := copyRegularFile(sourcePath, targetPath, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}

func copyRegularFile(sourcePath, targetPath string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return fmt.Errorf("copy parent create failed: %w", err)
	}
	src, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("copy source open failed: %w", err)
	}
	defer src.Close()
	dst, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return fmt.Errorf("copy destination open failed: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return fmt.Errorf("copy file failed: %w", err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("copy destination close failed: %w", err)
	}
	return nil
}

func rootfsPath(mountDir, guestDest string) (string, error) {
	targetPath := filepath.Join(mountDir, strings.TrimPrefix(guestDest, string(filepath.Separator)))
	if !pathWithin(mountDir, targetPath) {
		return "", fmt.Errorf("copy.dest escapes mounted rootfs: %s", guestDest)
	}
	return targetPath, nil
}

func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
