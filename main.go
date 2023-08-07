package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const version = "0.0.7"

var config = map[string]interface{}{
	"ignored_directories":     []string{".git"},
	"output_directory":        "public",
	"valid_binary_extensions": []string{".nro", ".elf", ".rpx", ".cia", ".3dsx", ".dol"},
}

func copy(src, dst string) error {
	sourceFileStat, err := os.Stat(src)
	if err != nil {
		return err
	}

	if !sourceFileStat.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", src)
	}

	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	_, err = os.Stat(dst)
	if err == nil {
		return fmt.Errorf("file %s already exists", dst)
	}

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	if err != nil {
		panic(err)
	}

	buf := make([]byte, 1024)
	for {
		n, err := source.Read(buf)
		if err != nil && err != io.EOF {
			return err
		}
		if n == 0 {
			break
		}

		if _, err := destination.Write(buf[:n]); err != nil {
			return err
		}
	}
	return err
}

func main() {
	underprint("This is Spinarak v" + version + " by CompuCat and the 4TU Team.")

	// Load config
	err := loadConfig()
	if err != nil {
		fmt.Println("Couldn't load config.json; using default configuration.")
	}

	// Instantiate output directory if needed and look for pre-existing libget repo.
	updatingRepo := false // This flag is true if the output directory is a valid libget repo.
	previousRepoJSON := map[string]interface{}{}
	if _, err := os.Stat(config["output_directory"].(string)); err == nil {
		dirEntries, _ := os.ReadDir(config["output_directory"].(string))
		if len(dirEntries) == 0 {
		} else {
			previousRepoJSON, err = loadJSONFile(filepath.Join(config["output_directory"].(string), "repo.json"))
			if err != nil {
				fmt.Println("ERROR: output directory is not empty and is not a libget repo. Stopping.")
				return
			}
			updatingRepo = true
			fmt.Println("INFO: the output directory is already a libget repo! Updating the existing repo.")
		}
	} else {
		os.MkdirAll(config["output_directory"].(string), os.ModePerm)
	}

	// Detect packages.
	pkgDirs := getPkgDirs()
	fmt.Printf("%d detected packages: %v\n\n", len(pkgDirs), pkgDirs)

	// Initialize JSON objects
	repoJSON := map[string]interface{}{
		"packages": []interface{}{},
	}

	// Package all the things
	for _, pkg := range pkgDirs {
		// Open and validate pkgbuild
		pkgBuild, err := loadJSONFile(filepath.Join(pkg, "pkgbuild.json"))
		if err != nil {
			fmt.Printf("ERROR: failed to build %s! Error message: %s\n\n", pkg, err)
			continue
		}

		if updatingRepo {
			prevPkgInfo := getPrevPkgInfo(pkgBuild["package"].(string), previousRepoJSON)
			if prevPkgInfo != nil {
				if prevPkgInfo["version"].(string) == pkgBuild["info"].(map[string]interface{})["version"].(string) {
					fmt.Printf("%s hasn't changed, skipping.\n\n", pkgBuild["info"].(map[string]interface{})["title"].(string))
					continue
				}
			}
		}

		manifest, err := os.Create(filepath.Join(pkg, "manifest.install"))
		if err != nil {
			fmt.Printf("ERROR: failed to create manifest for %s! Error message: %s\n\n", pkg, err)
			continue
		}

		fmt.Printf("%d asset(s) detected\n", len(pkgBuild["assets"].([]interface{})))
		for _, asset := range pkgBuild["assets"].([]interface{}) {
			handleAsset(pkg, asset.(map[string]interface{}), manifest)
		}

		pkgInfoTimestamp := pkgBuild["info"].(map[string]interface{})["timestamp"]
		if pkgInfoTimestamp == nil {
			fmt.Println("WARNING: no timestamp found!")
			pkgInfoTimestamp = time.Now().Unix()
			fmt.Println("WARNING: using current timestamp.")
		}

		pkgInfo := map[string]interface{}{
			"category":    pkgBuild["info"].(map[string]interface{})["category"].(string),
			"name":        pkgBuild["package"].(string),
			"license":     pkgBuild["info"].(map[string]interface{})["license"].(string),
			"title":       pkgBuild["info"].(map[string]interface{})["title"].(string),
			"url":         pkgBuild["info"].(map[string]interface{})["url"].(string),
			"author":      pkgBuild["info"].(map[string]interface{})["author"].(string),
			"version":     pkgBuild["info"].(map[string]interface{})["version"].(string),
			"details":     pkgBuild["info"].(map[string]interface{})["details"].(string),
			"description": pkgBuild["info"].(map[string]interface{})["description"].(string),
			"updated":     time.Unix(pkgInfoTimestamp.(int64), 0).Format("2006-01-02"),
		}

		pkgInfo["changelog"] = pkgBuild["changelog"]
		if pkgBuild["changes"] != nil {
			pkgInfo["changelog"] = pkgBuild["changes"]
			fmt.Println("WARNING: the `changes` field was deprecated from the start. Use `changelog` instead.")
		} else {
			fmt.Println("WARNING: no changelog found!")
		}

		infoJSONFile, err := os.Create(filepath.Join(pkg, "info.json"))
		if err != nil {
			fmt.Printf("ERROR: failed to create info.json for %s! Error message: %s\n\n", pkg, err)
			manifest.Close()
			continue
		}
		jsonEncoder := json.NewEncoder(infoJSONFile)
		jsonEncoder.SetIndent("", " ")
		jsonEncoder.Encode(pkgInfo)
		fmt.Println("info.json generated.")
		infoJSONFile.Close()

		manifest.Close()
		fmt.Printf("Package is %d KiB large.\n", getDirSize(pkg)/1024)

		outputZipPath := filepath.Join(config["output_directory"].(string), "zips", pkg+".zip")
		if _, err := os.Stat(outputZipPath); err != nil {
			os.MkdirAll(filepath.Dir(outputZipPath), os.ModePerm)
		}
		zipErr := zipDirectory(pkg, outputZipPath)
		if zipErr != nil {
			fmt.Printf("ERROR: failed to create zip archive for %s! Error message: %s\n\n", pkg, zipErr)
			continue
		}
		fmt.Printf("Package written to %s\n", outputZipPath)
		fmt.Printf("Zipped package is %d KiB large.\n\n", getFileSize(outputZipPath)/1024)

		repoExtendedInfo := map[string]interface{}{
			"extracted": getDirSize(pkg) / 1024,
			"filesize":  getFileSize(outputZipPath) / 1024,
			"web_dls":   -1, // TODO: get these counts from stats API
			"app_dls":   -1, // TODO
		}

		binaryPath := getBinaryPath(pkgBuild["info"].(map[string]interface{}), pkg)
		repoExtendedInfo["binary"] = binaryPath

		repoExtendedInfo = mergeMaps(pkgInfo, repoExtendedInfo)
		repoJSON["packages"] = append(repoJSON["packages"].([]interface{}), repoExtendedInfo)
	}

	repoJSONFile, err := os.Create(filepath.Join(config["output_directory"].(string), "repo.json"))
	if err != nil {
		fmt.Printf("ERROR: failed to create repo.json! Error message: %s\n", err)
		return
	}
	jsonEncoder := json.NewEncoder(repoJSONFile)
	jsonEncoder.SetIndent("", " ")
	jsonEncoder.Encode(repoJSON)
	fmt.Printf("%s generated.\n\n", filepath.Join(config["output_directory"].(string), "repo.json"))

	underprint("\nSUMMARY")
	fmt.Printf("Built %d of %d packages.\n", len(pkgDirs)-len(repoJSON["packages"].([]interface{})), len(pkgDirs))
	fmt.Printf("All done. Enjoy your new repo :)\n")
}

func underprint(x string) {
	fmt.Printf("%s\n%s\n", x, strings.Repeat("-", len(strings.TrimSpace(x))))
}

func loadConfig() error {
	file, err := os.Open("config.json")
	if err != nil {
		return err
	}
	defer file.Close()

	err = json.NewDecoder(file).Decode(&config)
	if err != nil {
		return err
	}
	return nil
}

func getPkgDirs() []string {
	pkgDirs := []string{}
	dirEntries, err := os.ReadDir(".")
	if err != nil {
		return pkgDirs
	}
	for _, entry := range dirEntries {
		if entry.IsDir() {
			ignored := false
			for _, ignoredDir := range config["ignored_directories"].([]interface{}) {
				if entry.Name() == ignoredDir {
					ignored = true
					break
				}
			}
			if !ignored {
				pkgBuildPath := filepath.Join(entry.Name(), "pkgbuild.json")
				if _, err := os.Stat(pkgBuildPath); err == nil {
					pkgDirs = append(pkgDirs, entry.Name())
				}
			}
		}
	}
	return pkgDirs
}

func loadJSONFile(filePath string) (map[string]interface{}, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var result map[string]interface{}
	err = json.NewDecoder(file).Decode(&result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func handleAsset(pkg string, asset map[string]interface{}, manifest *os.File) {
	if subAsset, ok := asset["subAsset"]; ok && subAsset.(bool) {
		fmt.Println("- Asset is subasset")
		// Subasset handling logic here
	} else {
		localPath := filepath.Join(pkg, asset["url"].(string))
		if _, err := os.Stat(localPath); err == nil {
			fmt.Println("- Asset is local")
			// Asset is local, copy it
			tmpFile, err := os.CreateTemp("", "asset_")
			if err != nil {
				fmt.Printf("ERROR: Failed to create temporary file for asset %s: %s\n", asset["url"].(string), err)
				return
			}
			defer tmpFile.Close()

			file, err := os.Open(localPath)
			if err != nil {
				fmt.Printf("ERROR: Failed to open local asset %s: %s\n", asset["url"].(string), err)
				return
			}
			defer file.Close()

			_, err = io.Copy(tmpFile, file)
			if err != nil {
				fmt.Printf("ERROR: Failed to copy local asset %s to temporary file: %s\n", asset["url"].(string), err)
				return
			}

			tmpFile.Seek(0, io.SeekStart)
			handleAssetType(pkg, tmpFile, asset, manifest)
		} else {
			fmt.Printf("- Downloading %s...\n", asset["url"].(string))
			resp, err := http.Get(asset["url"].(string))
			if err != nil {
				fmt.Printf("ERROR: Failed to download asset %s: %s\n", asset["url"].(string), err)
				return
			}
			defer resp.Body.Close()

			tmpFile, err := os.CreateTemp("", "asset_")
			if err != nil {
				fmt.Printf("ERROR: Failed to create temporary file for asset %s: %s\n", asset["url"].(string), err)
				return
			}
			defer tmpFile.Close()

			_, err = io.Copy(tmpFile, resp.Body)
			if err != nil {
				fmt.Printf("ERROR: Failed to save downloaded asset %s: %s\n", asset["url"].(string), err)
				return
			}

			tmpFile.Seek(0, io.SeekStart)
			handleAssetType(pkg, tmpFile, asset, manifest)
		}
	}
}

func handleAssetType(pkg string, file *os.File, asset map[string]interface{}, manifest *os.File) {
	prepend := "\t"

	if asset["type"].(string) == "update" || asset["type"].(string) == "get" || asset["type"].(string) == "local" || asset["type"].(string) == "extract" {
		// Handling for different asset types
		fmt.Printf("%s- Type is %s, moving to /%s\n", prepend, asset["type"].(string), asset["dest"].(string))
		manifest.WriteString(strings.ToUpper(asset["type"].(string)[:1]) + ": " + asset["dest"].(string) + "\n")
		destPath := filepath.Join(pkg, asset["dest"].(string))
		os.MkdirAll(filepath.Dir(destPath), os.ModePerm)

		destFile, err := os.Create(destPath)
		if err != nil {
			fmt.Printf("ERROR: Failed to create destination file for asset %s: %s\n", asset["url"].(string), err)
			return
		}
		defer destFile.Close()

		_, err = io.Copy(destFile, file)
		if err != nil {
			fmt.Printf("ERROR: Failed to copy asset content to destination %s: %s\n", destPath, err)
			return
		}
	} else if asset["type"].(string) == "icon" {
		// Handling for icon asset
		fmt.Printf("%s- Type is icon, moving to /icon.png\n", prepend)
		iconPath := filepath.Join(pkg, "icon.png")
		destPath := filepath.Join(config["output_directory"].(string), "packages", pkg, "icon.png")

		destFile, err := os.Create(iconPath)
		if err != nil {
			fmt.Printf("ERROR: Failed to create destination file for icon: %s\n", err)
			return
		}
		defer destFile.Close()

		_, err = io.Copy(destFile, file)
		if err != nil {
			fmt.Printf("ERROR: Failed to copy icon content to destination %s: %s\n", iconPath, err)
			return
		}

		os.MkdirAll(filepath.Dir(destPath), os.ModePerm)
		err = copy(iconPath, destPath)
		if err != nil {
			fmt.Printf("ERROR: Failed to copy icon to output directory: %s\n", err)
			return
		}
	} else if asset["type"].(string) == "screenshot" {
		// Handling for screenshot asset
		fmt.Printf("%s- Type is screenshot, moving to /screen.png\n", prepend)
		screenPath := filepath.Join(pkg, "screen.png")
		destPath := filepath.Join(config["output_directory"].(string), "packages", pkg, "screen.png")

		destFile, err := os.Create(screenPath)
		if err != nil {
			fmt.Printf("ERROR: Failed to create destination file for screenshot: %s\n", err)
			return
		}
		defer destFile.Close()

		_, err = io.Copy(destFile, file)
		if err != nil {
			fmt.Printf("ERROR: Failed to copy screenshot content to destination %s: %s\n", screenPath, err)
			return
		}

		os.MkdirAll(filepath.Dir(destPath), os.ModePerm)
		err = copy(screenPath, destPath)
		if err != nil {
			fmt.Printf("ERROR: Failed to copy screenshot to output directory: %s\n", err)
			return
		}
	} else if asset["type"].(string) == "zip" {
		// Handling for zip asset
		fmt.Printf("%s- Type is zip, has %d sub-asset(s)\n", prepend, len(asset["zip"].([]interface{})))

		tmpDir, err := os.MkdirTemp("", "zip_extract_")
		if err != nil {
			fmt.Printf("ERROR: Failed to create temporary directory for zip extraction: %s\n", err)
			return
		}
		defer os.RemoveAll(tmpDir)

		_, err = file.Seek(0, io.SeekStart)
		if err != nil {
			fmt.Printf("ERROR: Failed to seek to the beginning of zip file: %s\n", err)
			return
		}

		err = unzip(file, tmpDir)
		if err != nil {
			fmt.Printf("ERROR: Failed to unzip asset: %s\n", err)
			return
		}

		for _, subAsset := range asset["zip"].([]interface{}) {
			subAssetInfo := subAsset.(map[string]interface{})
			subAssetInfo["url"] = filepath.Join(tmpDir, subAssetInfo["path"].(string))
			handleAsset(pkg, subAssetInfo, manifest)
		}
	} else {
		fmt.Println("ERROR: Asset of unknown type detected. Skipping.")
	}
}

func unzip(src *os.File, destDir string) error {
	srcInfo, err := src.Stat()
	if err != nil {
		return err
	}
	zipReader, err := zip.NewReader(src, srcInfo.Size())
	if err != nil {
		return err
	}

	for _, zipFile := range zipReader.File {
		zipPath := filepath.Join(destDir, zipFile.Name)
		if zipFile.FileInfo().IsDir() {
			os.MkdirAll(zipPath, os.ModePerm)
			continue
		}

		destFile, err := os.Create(zipPath)
		if err != nil {
			return err
		}
		defer destFile.Close()

		srcFile, err := zipFile.Open()
		if err != nil {
			return err
		}
		defer srcFile.Close()

		_, err = io.Copy(destFile, srcFile)
		if err != nil {
			return err
		}
	}

	return nil
}

func getDirSize(directory string) int64 {
	var size int64
	err := filepath.WalkDir(directory, func(path string, info os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		fileInfo, err := info.Info()
		if err != nil {
			return err
		}
		size += fileInfo.Size()
		return nil
	})
	if err != nil {
		fmt.Println("Error calculating directory size:", err)
	}
	return size
}

func getFileSize(filePath string) int64 {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		fmt.Println("Error getting file size:", err)
		return 0
	}
	return fileInfo.Size()
}

func getPrevPkgInfo(pkgName string, previousRepoJSON map[string]interface{}) map[string]interface{} {
	packages := previousRepoJSON["packages"].([]interface{})
	for _, pkgInfo := range packages {
		if pkgInfo.(map[string]interface{})["name"].(string) == pkgName {
			return pkgInfo.(map[string]interface{})
		}
	}
	return nil
}

func mergeMaps(map1, map2 map[string]interface{}) map[string]interface{} {
	merged := make(map[string]interface{})
	for k, v := range map1 {
		merged[k] = v
	}
	for k, v := range map2 {
		merged[k] = v
	}
	return merged
}

func getBinaryPath(info map[string]interface{}, pkg string) string {
	if bin, ok := info["binary"]; ok {
		return bin.(string)
	}

	if info["category"].(string) == "theme" {
		return "none"
	}

	binPath := ""
	filepath.WalkDir(pkg, func(path string, info os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			for _, ext := range config["valid_binary_extensions"].([]interface{}) {
				if strings.HasSuffix(info.Name(), ext.(string)) {
					binPath = path
					return nil
				}
			}
		}
		return nil
	})
	if binPath == "" {
		fmt.Printf("WARNING: %s's binary path not specified in pkgbuild.json, and no binary found!\n", info["title"].(string))
	} else {
		fmt.Printf("WARNING: binary path not specified in pkgbuild.json; guessing %s.\n", binPath)
	}
	return binPath
}

func zipDirectory(sourceDir, zipFileName string) error {
	// Create the output zip file
	zipFile, err := os.Create(zipFileName)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	// Create a new zip archive
	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	// Walk through the directory and add files to the zip archive
	err = filepath.Walk(sourceDir, func(filePath string, fileInfo os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if fileInfo.IsDir() {
			return nil
		}

		// Open the file for reading
		file, err := os.Open(filePath)
		if err != nil {
			return err
		}
		defer file.Close()

		// Create a new file header
		zipHeader, err := zip.FileInfoHeader(fileInfo)
		if err != nil {
			return err
		}

		// Set the file name
		zipHeader.Name = filepath.Join(sourceDir, strings.TrimPrefix(filePath, sourceDir))

		// Create a new zip file entry
		zipEntry, err := zipWriter.CreateHeader(zipHeader)
		if err != nil {
			return err
		}

		// Copy the file content to the zip entry
		_, err = io.Copy(zipEntry, file)
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return err
	}

	return nil
}
