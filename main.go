package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Directory paths
const (
	baseDir   = "C:/me/sort" // Base directory
	inboxDir  = baseDir + "/inbox"
	sortedDir = baseDir + "/sorted"
	deleteDir = baseDir + "/delete"
)

// Helper function to calculate SHA-256 hash of a file
func fileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	_, err = io.Copy(hash, file)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// Function to collect hashes from sorted directory into a hash map
func collectSortedHashes() (map[string]string, error) {
	hashes := make(map[string]string)

	// Walk through the sorted directory and its subdirectories to collect file hashes
	err := filepath.Walk(sortedDir, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Calculate hash of the file and add to the map
		hash, err := fileHash(filePath)
		if err != nil {
			fmt.Printf("Error hashing file %s: %v\n", filePath, err)
			return nil
		}
		hashes[hash] = filePath
		return nil
	})

	return hashes, err
}

// Function to check for duplicate files in inbox and move them accordingly
func checkAndSortFiles() error {
	var wg sync.WaitGroup

	// Collect file hashes from the sorted directory
	sortedHashes, err := collectSortedHashes()
	if err != nil {
		return fmt.Errorf("Error collecting sorted file hashes: %v", err)
	}

	// Walking through the inbox directory and its subdirectories
	err = filepath.Walk(inboxDir, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories or hidden files (e.g., .DS_Store)
		if info.IsDir() || strings.HasPrefix(info.Name(), ".") {
			return nil
		}

		// Add a goroutine to process each file concurrently
		wg.Add(1)
		go func(filePath string) {
			defer wg.Done()

			// Calculate hash for the file in the inbox
			hash, err := fileHash(filePath)
			if err != nil {
				fmt.Printf("Error hashing file %s: %v\n", filePath, err)
				return
			}

			// Check if file already exists in sorted directory using the hash map
			if existingPath, found := sortedHashes[hash]; found {
				// If a duplicate is found, move to delete folder with metadata
				fmt.Printf("Duplicate found: %s already exists as %s\n", filePath, existingPath)
				moveFileWithMetadata(filePath, deleteDir)
			} else {
				// If no duplicate, move to sorted folder and add hash to the map
				moveFileBasedOnExtension(filePath)
				sortedHashes[hash] = filePath
			}
		}(filePath)

		return nil
	})

	wg.Wait()
	return err
}

// Function to move file to the destination folder
func moveFile(src, dest string) error {
	err := os.MkdirAll(dest, os.ModePerm)
	if err != nil {
		return err
	}

	// Get the current file's base name and extension
	ext := filepath.Ext(src)
	baseName := strings.TrimSuffix(filepath.Base(src), ext)

	// Check if the file already exists in the destination folder
	destFilePath := filepath.Join(dest, filepath.Base(src))
	if _, err := os.Stat(destFilePath); err == nil {
		// File exists, create a new name using the hash (first 6 characters)
		hash, err := fileHash(src)
		if err != nil {
			return err
		}
		hashPrefix := hash[:6] // First 6 characters of the hash
		newName := fmt.Sprintf("%s_%s%s", baseName, hashPrefix, ext)
		destFilePath = filepath.Join(dest, newName)
	}

	// Move the file to the destination
	err = os.Rename(src, destFilePath)
	if err != nil {
		return err
	}

	fmt.Printf("Moved file to %s\n", destFilePath)
	return nil
}

// Function to move file to the delete folder with metadata (hash-based name)
func moveFileWithMetadata(src, dest string) error {
	err := os.MkdirAll(dest, os.ModePerm)
	if err != nil {
		return err
	}

	// Get the current file's base name and extension
	ext := filepath.Ext(src)
	baseName := strings.TrimSuffix(filepath.Base(src), ext)

	// Calculate the hash for uniqueness
	hash, err := fileHash(src)
	if err != nil {
		return err
	}
	hashPrefix := hash[:6] // First 6 characters of the hash

	// Append the hash to the file name
	newName := fmt.Sprintf("%s_%s_processed_delete%s", baseName, hashPrefix, ext)

	destFilePath := filepath.Join(dest, newName)
	err = os.Rename(src, destFilePath)
	if err != nil {
		return err
	}

	fmt.Printf("Moved file to %s with metadata\n", destFilePath)
	return nil
}

// Function to move file based on its extension
func moveFileBasedOnExtension(filePath string) {
	ext := strings.ToLower(filepath.Ext(filePath))
	ext = strings.TrimPrefix(ext, ".") // Remove the leading dot

	// Default folder if no specific category is found
	categoryFolder := "Miscellaneous"

	// Check if the file extension has a defined category
	extCategories := map[string]string{
		"iso":  "ISO_Files",
		"pdf":  "PDF_Files",
		"txt":  "Text_Files",
		"jpg":  "Images",
		"jpeg": "Images",
		"png":  "Images",
		"doc":  "Office_Docs",
		"docx": "Office_Docs",
		"xls":  "Office_Docs",
		"xlsx": "Office_Docs",
	}

	// Check if the file extension has a defined category
	if folder, found := extCategories[ext]; found {
		categoryFolder = folder
	}

	// Move the file to the appropriate folder
	destFolder := filepath.Join(sortedDir, categoryFolder)
	moveFile(filePath, destFolder)
}

func main() {
	err := checkAndSortFiles()
	if err != nil {
		fmt.Println("Error while sorting files:", err)
	} else {
		fmt.Println("File sorting completed successfully.")
	}
}
