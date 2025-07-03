# Sorter
A minimal tool to organize files by extension and detect duplicates. Files are sorted into configurable categories and duplicates are moved to a separate directory.

### Usage
1. Place files to sort in `inbox` directory
2. Run the program

Files will be:
* Sorted into `sorted` by extension
* Duplicates moved to `delete`
* Empty/invalid files skipped

### Directory Structure
```
baseDir/
├── inbox/      # Input files
├── sorted/     # Organized output
└── delete/     # Duplicate files
```