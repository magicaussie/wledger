package images

import (
	"fmt"
	"image"
	"image/jpeg"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/disintegration/imaging"
)

const (
	UploadDir = "./app/uploads"
)

func Init() error {
	return os.MkdirAll(UploadDir, 0755)
}

// ProcessUpload handles the file upload, resize, and saving
// Returns the relative path to the saved image
func ProcessUpload(file multipart.File, header *multipart.FileHeader) (string, error) {
	// Decode image
	img, err := imaging.Decode(file)
	if err != nil {
		return "", fmt.Errorf("failed to decode image: %w", err)
	}

	// Generate Unique Name
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
		return "", fmt.Errorf("unsupported file type: %s", ext)
	}

	timestamp := time.Now().UnixNano()
	baseName := fmt.Sprintf("part_%d", timestamp)
	fileName := baseName + ".jpg" // convert everything to JPG for consistency

	// Process Main Image (Resize if too large, e.g. > 1024px)
	// Fit fits the image within the box, preserving aspect ratio
	mainImg := imaging.Fit(img, 1024, 1024, imaging.Lanczos)

	// Process Thumbnail (Fixed square crop for UI lists)
	thumbImg := imaging.Thumbnail(img, 300, 300, imaging.Lanczos)

	// Save Files
	if err := saveJPG(mainImg, fileName); err != nil {
		return "", err
	}
	if err := saveJPG(thumbImg, baseName+"_thumb.jpg"); err != nil {
		return "", err
	}

	return fileName, nil
}

func saveJPG(img image.Image, name string) error {
	path := filepath.Join(UploadDir, name)
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	// High quality JPG
	return jpeg.Encode(out, img, &jpeg.Options{Quality: 85})
}

// Delete removes the image and its thumbnail.
func Delete(fileName string) {
	if fileName == "" {
		return
	}
	os.Remove(filepath.Join(UploadDir, fileName))

	// Try to remove thumb (assumes standard naming convention)
	ext := filepath.Ext(fileName)
	base := strings.TrimSuffix(fileName, ext)
	os.Remove(filepath.Join(UploadDir, base+"_thumb.jpg"))
}
