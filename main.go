package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Helper function to determine the base directory based on the operating system
func getBaseDir() string {
	switch runtime.GOOS {
	case "windows":
		return "C:/me/sort" // Base directory for Windows
	case "darwin":
		return "/Volumes/CCC" // Base directory for macOS
	default:
		return "C:/me/sort" // Default to Linux or other OS
	}
}

// Directory paths
var (
	baseDir   = getBaseDir() // Dynamically set base directory
	inboxDir  = baseDir + "/inbox"
	sortedDir = baseDir + "/sorted"
	deleteDir = baseDir + "/delete"
)

var (
	excludeDirPatterns = []string{
		".git", ".svn", ".hg",
		".idea", ".vscode",
		"node_modules", "__pycache__",
		"__MACOSX",
		"*.app", "*.kext", "*.framework",
		"*.bundle", "*.plugin",
		"System Volume Information",
		"lost+found",
		"bin", "obj", "target", "build", "dist", // Build artifacts
	}

	excludeFilePatterns = []string{
		"*.tmp", "*.bak", "*.~", "~*", // Temporary/backup files
		"*.dll", "*.sys", // Windows system files
		"*.log", "*.dmp", // Logs and dumps
		"*.swp", "*.swo", // Vim swap files
	}
)

// Load extension categories from JSON config
func loadExtensionCategories() (map[string]string, error) {
	configPath := filepath.Join("extensions.json")

	file, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open extension config: %w", err)
	}
	defer file.Close()

	var categories map[string]string
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&categories); err != nil {
		return nil, fmt.Errorf("invalid extension config format: %w", err)
	}

	return categories, nil
}

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
	// Collect file hashes from the sorted directory
	sortedHashes, err := collectSortedHashes()
	if err != nil {
		return fmt.Errorf("Error collecting sorted file hashes: %v", err)
	}

	// Map to track processed hashes to avoid duplicates during the current run
	processedHashes := make(map[string]bool)

	// Walking through the inbox directory and its subdirectories
	err = filepath.Walk(inboxDir, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories or hidden files (e.g., .DS_Store)
		if info.IsDir() {
			dirName := info.Name()

			// Check exclusion patterns first
			for _, pattern := range excludeDirPatterns {
				matched, err := filepath.Match(pattern, dirName)
				if err != nil {
					fmt.Printf("Pattern error %q: %v\n", pattern, err)
					continue
				}
				if matched {
					fmt.Printf("Skipping excluded directory: %s\n", filePath)
					return filepath.SkipDir
				}
			}

			// Skip hidden directories (including .git)
			if strings.HasPrefix(dirName, ".") {
				fmt.Printf("Skipping hidden directory: %s\n", filePath)
				return filepath.SkipDir
			}

			// Important: Return here to prevent processing directories as files
			return nil
		}

		fileName := info.Name()

		// Skip hidden files and macOS extended attributes
		if strings.HasPrefix(fileName, ".") {
			if runtime.GOOS == "darwin" && strings.HasPrefix(fileName, "._") {
				fmt.Printf("Skipping macOS extended attribute file: %s\n", filePath)
			}
			return nil
		}

		// Skip excluded file patterns
		for _, pattern := range excludeFilePatterns {
			matched, err := filepath.Match(pattern, fileName)
			if err == nil && matched {
				fmt.Printf("Skipping excluded file: %s\n", filePath)
				return nil
			}
		}

		// Skip system files based on OS
		switch runtime.GOOS {
		case "windows":
			lowerName := strings.ToLower(fileName)
			switch lowerName {
			case "pagefile.sys", "hiberfil.sys", "swapfile.sys",
				"thumbs.db", "desktop.ini":
				fmt.Printf("Skipping Windows system file: %s\n", filePath)
				return nil
			}
		case "darwin":
			switch fileName {
			case "swapfile", "sleepimage", ".DS_Store",
				".Spotlight-V100", ".fseventsd":
				fmt.Printf("Skipping macOS system file: %s\n", filePath)
				return nil
			}
		}

		// Skip files that are empty
		if info.Size() == 0 {
			fmt.Printf("Skipping empty file: %s\n", filePath)
			return nil
		}

		// Check for invalid or unsafe characters in file names to prevent issues on certain operating systems
		if strings.ContainsAny(info.Name(), `<>:"/\|?*`) {
			fmt.Printf("Skipping file with invalid characters: %s\n", filePath)
			return nil
		}

		// Skip symbolic links to avoid processing unintended files or creating loops
		if info.Mode()&os.ModeSymlink != 0 {
			fmt.Printf("Skipping symbolic link: %s\n", filePath)
			return nil
		}

		// Log the file being processed
		fmt.Printf("Processing file: %s\n", filePath)

		// Calculate hash for the file in the inbox
		hash, err := fileHash(filePath)
		if err != nil {
			fmt.Printf("Error hashing file %s: %v\n", filePath, err)
			return nil
		}

		// Check if the file has already been processed in this run
		if processedHashes[hash] {
			fmt.Printf("Duplicate detected within run: %s\n", filePath)
			moveFileWithMetadata(filePath, deleteDir)
			return nil
		}

		// Check if file already exists in sorted directory using the hash map
		if existingPath, found := sortedHashes[hash]; found {
			// If a duplicate is found, move to delete folder with metadata
			fmt.Printf("Duplicate found: %s already exists as %s\n", filePath, existingPath)
			moveFileWithMetadata(filePath, deleteDir)
		} else {
			// If no duplicate, move to sorted folder and add hash to the map
			fmt.Printf("File is unique, moving to sorted folder: %s\n", filePath)
			moveFileBasedOnExtension(filePath)
			sortedHashes[hash] = filePath
		}

		// Mark the hash as processed for this run
		processedHashes[hash] = true

		return nil
	})

	return err
}

// Function to move file to the destination folder
func moveFile(src, dest string) error {
	fmt.Printf("Moving file: %s to folder: %s\n", src, dest)

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

	fmt.Printf("File successfully moved to: %s\n", destFilePath)
	return nil
}

// Function to move file to the delete folder with metadata (hash-based name)
func moveFileWithMetadata(src, dest string) error {
	fmt.Printf("Moving file to delete folder with metadata: %s\n", src)

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

	fmt.Printf("File successfully moved to delete folder: %s\n", destFilePath)
	return nil
}

// Function to move file based on its extension
func moveFileBasedOnExtension(filePath string) {
	fmt.Printf("Sorting file by extension: %s\n", filePath)

	ext := strings.ToLower(filepath.Ext(filePath))
	ext = strings.TrimPrefix(ext, ".") // Remove the leading dot

	// Load categories from config
	extCategories, err := loadExtensionCategories()
	if err != nil {
		log.Printf("Error loading extension categories: %v", err)
		extCategories = make(map[string]string) // Use empty map as fallback
	}

	// Default folder if no specific category is found
	categoryFolder := "Miscellaneous"

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
