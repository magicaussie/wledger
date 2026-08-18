package images

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/disintegration/imaging"
	"github.com/tuxedocurly/wledger/internal/config"

	// Register decoders for formats suppliers commonly serve (e.g. Altronics
	// uses .webp). imaging.Decode relies on the image/* default decoders.
	_ "golang.org/x/image/webp"
)

func Init() error {
	// Ensure directory exists on startup
	return os.MkdirAll(config.DirUploadsImages, 0755)
}

// ProcessUpload handles the file upload, resize, and saving
// Returns the filename (e.g. "part_12345.jpg").
// The handler is responsible for prepending the web path.
func ProcessUpload(file multipart.File, header *multipart.FileHeader) (string, error) {
	// Decode image
	img, err := imaging.Decode(file)
	if err != nil {
		return "", fmt.Errorf("failed to decode image: %w", err)
	}

	// Validate extension
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp" {
		return "", fmt.Errorf("unsupported file type: %s", ext)
	}

	timestamp := time.Now().UnixNano()
	baseName := fmt.Sprintf("part_%d", timestamp)
	fileName := baseName + ".jpg" // Standardize to JPG

	// Main Image (Fit within 1024x1024)
	mainImg := imaging.Fit(img, 1024, 1024, imaging.Lanczos)

	// Thumbnail (Square crop 300x300)
	thumbImg := imaging.Thumbnail(img, 300, 300, imaging.Lanczos)

	// Save
	if err := saveJPG(mainImg, fileName); err != nil {
		return "", err
	}
	if err := saveJPG(thumbImg, baseName+"_thumb.jpg"); err != nil {
		return "", err
	}

	return fileName, nil
}

func saveJPG(img image.Image, name string) error {
	path := filepath.Join(config.DirUploadsImages, name)
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	return jpeg.Encode(out, img, &jpeg.Options{Quality: 85})
}

// ProcessBytes decodes an image from raw bytes, resizes/saves it exactly like
// ProcessUpload, and returns the generated filename (e.g. "part_12345.jpg").
// The caller is responsible for prepending the web path. Used to store remote
// supplier images onto the local filesystem.
func ProcessBytes(data []byte) (string, error) {
	img, err := imaging.Decode(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("failed to decode image: %w", err)
	}

	timestamp := time.Now().UnixNano()
	baseName := fmt.Sprintf("part_%d", timestamp)
	fileName := baseName + ".jpg"

	mainImg := imaging.Fit(img, 1024, 1024, imaging.Lanczos)
	thumbImg := imaging.Thumbnail(img, 300, 300, imaging.Lanczos)

	if err := saveJPG(mainImg, fileName); err != nil {
		return "", err
	}
	if err := saveJPG(thumbImg, baseName+"_thumb.jpg"); err != nil {
		return "", err
	}

	return fileName, nil
}

// DeleteByWebPath takes the full web path (e.g., "/uploads/images/part_123.jpg")
// and removes the corresponding files from the file system.
func DeleteByWebPath(webPath string) {
	if webPath == "" {
		return
	}

	// Extract filename: "/uploads/images/part_123.jpg" -> "part_123.jpg"
	fileName := filepath.Base(webPath)

	// Safety check: avoids accidentally trying to delete directories
	if fileName == "." || fileName == "/" || fileName == "images" {
		return
	}

	// Delete Main Image
	os.Remove(filepath.Join(config.DirUploadsImages, fileName))

	// Delete Thumbnail
	ext := filepath.Ext(fileName)
	base := strings.TrimSuffix(fileName, ext)
	os.Remove(filepath.Join(config.DirUploadsImages, base+"_thumb.jpg"))
}
