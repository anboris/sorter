package main

import (
	"encoding/json"
	"fmt"
	"github.com/cespare/xxhash/v2"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Helper function to determine the base directory based on the operating system
func getBaseDir() string {
	switch runtime.GOOS {
	case "windows":
		return "C:/me/sort" // Base directory for Windows
	case "darwin":
		return "/Users/andrew/sort" // Base directory for macOS
	default:
		return "C:/me/sort" // Default to Linux or other OS
	}
}

// Category configuration structures
type CategoryConfig map[string]CategoryGroup

type CategoryGroup struct {
	Extensions    []string                 `json:"extensions,omitempty"`
	Subcategories map[string]CategoryGroup `json:"subcategories,omitempty"`
}

type ExclusionConfig struct {
	Common     []string            `json:"common"`
	OSSpecific map[string][]string `json:"os_specific"`
}

// Directory paths
var (
	baseDir      = getBaseDir() // Dynamically set base directory
	inboxDir     = baseDir + "/inbox"
	sortedDir    = baseDir + "/sorted"
	deleteDir    = baseDir + "/delete"
	extensionMap = make(map[string]string)
	configLoaded bool
	configMutex  sync.Mutex
	excludeDirs  []string
	excludeFiles []string
)

func loadExclusionConfig() error {
	// Load directory exclusions
	dirExclPath := filepath.Join("dir_exclusions.json")
	if err := loadExclusionFile(dirExclPath, &excludeDirs); err != nil {
		return err
	}

	// Load file exclusions
	fileExclPath := filepath.Join("file_exclusions.json")
	return loadExclusionFile(fileExclPath, &excludeFiles)
}

func loadExclusionFile(path string, target *[]string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open exclusion config: %w", err)
	}
	defer file.Close()

	var config ExclusionConfig
	if err := json.NewDecoder(file).Decode(&config); err != nil {
		return fmt.Errorf("invalid exclusion config format: %w", err)
	}

	*target = append(config.Common, config.OSSpecific[runtime.GOOS]...)
	return nil
}

func init() {
	if err := loadExtensionConfig(); err != nil {
		log.Fatalf("Failed to load extension config: %v", err)
	}
	if err := loadExclusionConfig(); err != nil {
		log.Fatalf("Failed to load exclusion config: %v", err)
	}
}

func loadExtensionConfig() error {
	configPath := filepath.Join("extensions.json")

	file, err := os.Open(configPath)
	if err != nil {
		return fmt.Errorf("failed to open extension config: %w", err)
	}
	defer file.Close()

	var config CategoryConfig
	if err := json.NewDecoder(file).Decode(&config); err != nil {
		return fmt.Errorf("invalid extension config format: %w", err)
	}

	configMutex.Lock()
	defer configMutex.Unlock()
	extensionMap = buildExtensionMap(config)
	configLoaded = true

	return nil
}

func buildExtensionMap(config CategoryConfig) map[string]string {
	extMap := make(map[string]string)

	for mainCategory, group := range config {
		processCategoryGroup(mainCategory, group, extMap)
	}

	// Add special case for macOS attribute files
	extMap["_"] = "System/Attribute_Files" // For ._ prefix files

	return extMap
}

func processCategoryGroup(currentPath string, group CategoryGroup, extMap map[string]string) {
	// Process current level extensions
	for _, ext := range group.Extensions {
		extMap[strings.ToLower(ext)] = currentPath
	}

	// Process subcategories
	for subName, subGroup := range group.Subcategories {
		subPath := filepath.Join(currentPath, subName)
		processCategoryGroup(subPath, subGroup, extMap)
	}
}

// Helper function to calculate XXH64 hash of a file
func fileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := xxhash.New()
	_, err = io.Copy(hash, file)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum64()), nil
}

// Helper function to print progress
func printProgress(current, total int) {
	fmt.Printf("\rProcessing: %d/%d (%.0f%%)", current, total, float64(current)/float64(total)*100)
	os.Stdout.Sync() // Force flush the output
}

// Function to collect hashes from sorted directory into a hash map
func collectSortedHashes() (map[string]string, error) {
	start := time.Now()
	hashes := make(map[string]string)
	var totalFiles int
	var processedFiles int

	defer func() {
		duration := time.Since(start)
		fmt.Printf(
			"Processing completed in %v (%.1f files/sec)\n",
			duration.Round(time.Second),
			float64(totalFiles)/duration.Seconds(),
		)
	}()

	// FIRST PASS: Count total files
	err := filepath.Walk(sortedDir, func(filePath string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			totalFiles++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if totalFiles == 0 {
		fmt.Println("No files found in sorted directory")
		return hashes, nil
	}

	// Clear any previous output before starting progress
	fmt.Print("\033[2K\r") // ANSI escape code to clear line
	fmt.Printf("Indexing %d files in sorted directory...\n", totalFiles)

	// SECOND PASS: Walk through the sorted directory to collect file hashes
	err = filepath.Walk(sortedDir, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		processedFiles++
		printProgress(processedFiles, totalFiles)

		hash, err := fileHash(filePath)
		if err != nil {
			// Print error on new line to not break progress bar
			fmt.Printf("\nError hashing file %s: %v\n", filePath, err)
			printProgress(processedFiles, totalFiles) // Redraw progress bar
			return nil
		}
		hashes[hash] = filePath
		return nil
	})

	fmt.Println() // New line after progress bar
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
			for _, pattern := range excludeDirs {
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
		for _, pattern := range excludeFiles {
			matched, err := filepath.Match(pattern, fileName)
			if err != nil {
				fmt.Printf("Pattern error %q: %v\n", pattern, err)
				continue
			}
			if matched {
				fmt.Printf("Skipping excluded file: %s (matched pattern: %s)\n", filePath, pattern)
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

// Updated file sorting logic
func moveFileBasedOnExtension(filePath string) {
	ext := strings.ToLower(filepath.Ext(filePath))
	baseName := filepath.Base(filePath)

	// Handle macOS extended attributes
	if strings.HasPrefix(baseName, "._") && runtime.GOOS == "darwin" {
		ext = "._*"
	}

	ext = strings.TrimPrefix(ext, ".")
	if ext == "" {
		ext = "no_extension"
	}

	configMutex.Lock()
	defer configMutex.Unlock()

	var categoryPath string
	if path, exists := extensionMap[ext]; exists {
		categoryPath = path
	} else {
		// Create misc subcategory based on extension type
		categoryPath = filepath.Join("Misc", strings.ToUpper(ext))
	}

	destFolder := filepath.Join(sortedDir, categoryPath)
	moveFile(filePath, destFolder)
}

// Function to scan and remove empty folders in the inbox directory after sorting
func removeEmptyDirs(root string) error {
	return filepath.Walk(root, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			fmt.Printf("Skipping %s due to error: %v\n", path, err)
			return nil
		}
		if path == root || !info.IsDir() {
			return nil
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil // skip if we can't read
		}
		if len(entries) == 0 {
			fmt.Printf("Removing empty folder: %s\n", path)
			return os.Remove(path)
		}
		return nil
	})
}

func main() {
	err := checkAndSortFiles()
	if err != nil {
		fmt.Println("Error while sorting files:", err)
	} else {
		fmt.Println("File sorting completed successfully.")
	}
	err = removeEmptyDirs(inboxDir)
	if err != nil {
		fmt.Printf("Error cleaning empty folders: %s", err)
	}
}
