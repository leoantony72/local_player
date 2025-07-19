package main

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	_ "embed" // Ensures the embed package is available for the //go:embed directive

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3" // SQLite driver import, aliased with underscore for side effects only
)

//go:embed index.hbs
var indexHTML []byte // Embeds the index.hbs file directly into the Go executable

// Metadata struct defines the structure for storing video file information in the database and JSON responses.
type Metadata struct {
	FileName string `json:"File_name"` // The name of the video file (e.g., "my_movie.mp4")
	Path     string `json:"Path"`   // The relative path from the app's root to the file (e.g., "videos/action/my_movie.mp4")
	Folder   string `json:"Folder"` // The relative path to the folder containing the file (e.g., "videos/action")
}

var sdb *sql.DB // Global variable to hold the database connection pool

func main() {
	// Initialize Gin. For production, you might want to set gin.ReleaseMode.
	// gin.SetMode(gin.ReleaseMode) // Uncomment this line for production builds to reduce log verbosity

	dbInit()      // Step 1: Initialize or open the SQLite database
	movies := getData() // Step 2: Scan the file system for video files
	saveData(movies)    // Step 3: Save the discovered video metadata into the database

	r := gin.Default() // Create a new Gin router instance

	// Serve video files and other static assets.
	// Files located relative to where the executable runs are accessible via the "/public/" URL path.
	// Example: A video at "./videos/my_movie.mp4" on disk will be served at "http://localhost:8000/public/videos/my_movie.mp4".
	r.Static("/public", "./")

	// Define the route for the root URL ("/"). It serves the embedded index.hbs file.
	r.GET("/", func(c *gin.Context) {
		c.Data(200, "text/html; charset=utf-8", indexHTML) // Serve the embedded HTML directly
	})

	// API endpoint to retrieve the content (subfolders and files) of a specified folder path.
	// The "/*folderpath" wildcard captures the dynamic part of the URL, allowing for nested paths.
	r.GET("/api/folder/*folderpath", getFolderContent)

	// API endpoint to search for video files by their file name.
	r.GET("/api/search/:name", searchFiles)

	log.Println("Server starting on :8000. Access your video library at http://localhost:8000")
	log.Fatal(r.Run(":8000")) // Start the Gin HTTP server on port 8000. log.Fatal will cause the program to exit if the server fails to start.
}

// dbInit initializes or opens the SQLite database file and ensures the 'movies' table exists with the correct schema.
func dbInit() {
	db, err := sql.Open("sqlite3", "movies.db") // Open (or create) the SQLite database file
	if err != nil {
		log.Fatalf("Fatal: Could not open database 'movies.db': %v", err) // Log a fatal error and exit if DB cannot be opened
	}

	createTableSQL := `
		CREATE TABLE IF NOT EXISTS movies (
			file_name TEXT UNIQUE NOT NULL, -- The name of the video file, must be unique
			path TEXT NOT NULL,         -- The full relative path to the video file
			folder TEXT NOT NULL        -- The relative path to the folder containing the video file
		);`
	// Execute the SQL to create the table if it doesn't already exist.
	if _, err := db.Exec(createTableSQL); err != nil {
		log.Fatalf("Fatal: Could not create 'movies' table: %v", err) // Log a fatal error and exit if table cannot be created
	}
	sdb = db // Assign the established database connection to the global variable
	log.Println("Database 'movies.db' initialized and table checked successfully.")
}

// getData walks the file system starting from the current directory (where the executable resides).
// It identifies .mp4 and .mkv video files, extracts their metadata, and normalizes paths.
func getData() []Metadata {
	var data []Metadata // Slice to store collected video metadata
	root := "."         // Defines the root directory for the file system walk

	log.Println("Starting file system scan for video files from:", root)
	// filepath.Walk traverses the directory tree rooted at 'root'.
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Log warnings for inaccessible paths but continue the walk to collect other files.
			log.Printf("Warning: Skipping inaccessible path %q: %v", path, err)
			return nil
		}
		
		// Process only regular files (not directories) with video extensions.
		if !info.IsDir() {
			ext := strings.ToLower(filepath.Ext(path)) // Get file extension in lowercase
			if ext == ".mp4" || ext == ".mkv" {        // Check if it's a supported video format
				relPath, relErr := filepath.Rel(".", path) // Get path relative to the current directory
				if relErr != nil {
					log.Printf("Warning: Could not determine relative path for %q: %v", path, relErr)
					return nil // Skip files for which relative path cannot be determined
				}
				folder := filepath.Dir(relPath) // Get the directory part of the relative path

				// Normalize all paths to use forward slashes ('/') for consistency across operating systems.
				// This is crucial for consistent database queries (LIKE patterns).
				normalizedRelPath := strings.ReplaceAll(relPath, string(os.PathSeparator), "/")
				normalizedFolder := strings.ReplaceAll(folder, string(os.PathSeparator), "/")
				
				// Append the collected and normalized metadata to our data slice.
				data = append(data, Metadata{
					FileName: info.Name(),
					Path:     normalizedRelPath,
					Folder:   normalizedFolder,
				})
			}
		}
		return nil // Continue the walk
	})

	if err != nil {
		log.Printf("Error encountered during file system walk: %v", err)
	}
	log.Printf("File system scan complete. Found %d video files to save/update.", len(data))
	return data
}

// saveData prepares and executes SQL statements to insert or update movie metadata in the database.
// It uses "INSERT OR IGNORE" to prevent adding duplicate entries based on the unique 'file_name' constraint.
func saveData(data []Metadata) {
	// Prepare a SQL statement once for efficiency, rather than preparing in each loop iteration.
	stmt, err := sdb.Prepare("INSERT OR IGNORE INTO movies (file_name, path, folder) VALUES (?,?,?);")
	if err != nil {
		log.Fatalf("Fatal: Error preparing SQL statement for saving data: %v", err)
	}
	defer stmt.Close() // Ensure the prepared statement is closed after the function returns

	countInserted := 0 // Counter for successfully inserted/updated rows
	for _, d := range data {
		result, execErr := stmt.Exec(d.FileName, d.Path, d.Folder)
		if execErr != nil {
			log.Printf("Warning: Error inserting/ignoring data for file %s: %v", d.FileName, execErr)
			continue // Continue to the next item even if one fails
		}
		rowsAffected, _ := result.RowsAffected() // Get the number of rows affected by the operation
		if rowsAffected > 0 { // If rowsAffected is 0, it means the row already existed and was ignored.
			countInserted++
		}
	}
	log.Printf("Successfully saved/updated %d unique video entries in the database.", countInserted)
}

// getFolderContent is a Gin handler that responds to API requests for listing contents of a specific folder.
// It returns a JSON object containing direct subfolders and video files within the requested folder.
func getFolderContent(c *gin.Context) {
	// Extract the folder path from the URL parameter and clean leading/trailing slashes.
	folder := strings.TrimPrefix(c.Param("folderpath"), "/")
	if folder == "" { // If the extracted path is empty, it means the request is for the root directory.
		folder = "."
	}
	// Normalize the incoming folder path (from URL) to use forward slashes for consistency with DB.
	folder = strings.ReplaceAll(folder, string(os.PathSeparator), "/")

	log.Printf("Incoming Request: getFolderContent for folder: %q", folder)

	// --- Logic to Find Direct Subfolders ---
	subfolders := make(map[string]bool) // Use a map to automatically handle uniqueness of folder names

	// Construct a SQL LIKE pattern to query all 'folder' paths that are descendants of the current 'folder'.
	// If 'folder' is ".", it queries all existing folders to find top-level ones.
	// ESCAPE '/' ensures that '/' in the pattern is treated literally and not as a wildcard (though usually not needed for '/')
	var queryPattern string
	if folder == "." {
		queryPattern = "%" // Matches any folder path
	} else {
		queryPattern = folder + "/%" // Matches folder paths starting with "current_folder/"
	}

	rows, err := sdb.Query("SELECT DISTINCT folder FROM movies WHERE folder LIKE ? ESCAPE '/'", queryPattern)
	if err != nil {
		log.Printf("Error querying potential subfolders for %q: %v", folder, err)
		c.JSON(500, gin.H{"error": "Failed to retrieve subfolders from database."})
		return
	}
	defer rows.Close() // Ensure database rows are closed when function exits

	for rows.Next() {
		var dbFolder string // A folder path retrieved from the database (e.g., "movies/action", "movies/action/thriller")
		if err := rows.Scan(&dbFolder); err != nil {
			log.Printf("Error scanning subfolder row from DB: %v", err)
			continue
		}
		
		// Skip the current folder itself if it happens to be returned by the broad query.
		if dbFolder == folder {
			continue
		}

		// Logic to extract only the IMMEDIATE child folder name (e.g., "action" from "movies/action/thriller").
		var potentialChild string
		if folder == "." {
			// If current folder is root ("."): the direct child is the first segment of any found folder path.
			// Example: if dbFolder is "Action/Season1", then "Action" is the direct child.
			parts := strings.Split(dbFolder, "/")
			if len(parts) > 0 && parts[0] != "" {
				potentialChild = parts[0]
			}
		} else {
			// If current folder is a subfolder (e.g., "movies"):
			// First, ensure dbFolder is actually a descendant (starts with "movies/").
			if !strings.HasPrefix(dbFolder, folder+"/") {
				continue // Skip if it's not a descendant.
			}
			// Then, trim the parent prefix (e.g., "movies/action/thriller" becomes "action/thriller").
			trimmed := strings.TrimPrefix(dbFolder, folder+"/")
			// Split by the first "/" to get the immediate child (e.g., "action" from "action/thriller").
			parts := strings.SplitN(trimmed, "/", 2)
			if len(parts) > 0 && parts[0] != "" {
				potentialChild = parts[0]
			}
		}

		if potentialChild != "" {
			subfolders[potentialChild] = true // Add to map; duplicates are automatically handled.
		}
	}
	if err := rows.Err(); err != nil { // Check for errors that might have occurred during iteration over rows
        log.Printf("Error during subfolder row iteration: %v", err)
    }

	// Convert the map of unique subfolder names into a sorted slice for consistent ordering in JSON.
	folderList := []string{}
	for k := range subfolders {
		folderList = append(folderList, k)
	}
	sort.Strings(folderList) // Sort alphabetically
	log.Printf("  Identified %d unique direct subfolders for %q: %v", len(folderList), folder, folderList)


	// --- Logic to Find Files Directly in the Current Folder ---
	// Query the database for all movie entries whose 'folder' field exactly matches the current 'folder'.
	fileRows, err := sdb.Query("SELECT file_name, path, folder FROM movies WHERE folder=?", folder)
	if err != nil {
		log.Printf("Error querying files for %q: %v", folder, err)
		c.JSON(500, gin.H{"error": "Failed to retrieve files from database."})
		return
	}
	defer fileRows.Close() // Ensure database rows are closed

	var files []Metadata // Slice to store file metadata
	for fileRows.Next() {
		m := Metadata{}
		if err := fileRows.Scan(&m.FileName, &m.Path, &m.Folder); err != nil {
			log.Printf("Error scanning file row from DB: %v", err)
			continue
		}
		files = append(files, m)
	}
	if err := fileRows.Err(); err != nil { // Check for errors during file row iteration
        log.Printf("Error during file row iteration: %v", err)
    }
	log.Printf("  Found %d files directly in folder %q.", len(files), folder)

	// Determine the parent path for the frontend's "Back" button and breadcrumbs.
	parentPath := filepath.Dir(folder)
	// Special handling for the root's parent: if filepath.Dir returns "/" or "..", map it to ".".
	if parentPath == string(os.PathSeparator) || parentPath == ".." || parentPath == "" {
		parentPath = "."
	}
	// Normalize the parent path to forward slashes.
	parentPath = strings.ReplaceAll(parentPath, string(os.PathSeparator), "/")


	// Send the response as a JSON object containing the lists of folders and files, and navigation info.
	c.JSON(200, gin.H{
		"folders": folderList, // List of direct subfolder names
		"files":   files,      // List of files in the current folder
		"parent":  parentPath, // Path to the parent folder
		"cwd":     folder,     // Current working directory path
	})
}

// searchFiles is a Gin handler that responds to API requests for searching video files by name.
// It performs a case-insensitive LIKE search on the 'file_name' column in the database.
func searchFiles(c *gin.Context) {
	queryParam := c.Param("name") // Get the search query from the URL parameter (e.g., "/api/search/movie")
	if queryParam == "" {
		c.JSON(400, gin.H{"error": "Search query cannot be empty."})
		return
	}
	query := "%" + queryParam + "%" // Prepare the query string with wildcards for SQL LIKE comparison
	log.Printf("Request: Searching for files with query: %q", queryParam)

	// Query the database for files whose name matches the search query.
	rows, err := sdb.Query("SELECT file_name, path, folder FROM movies WHERE file_name LIKE ?", query)
	if err != nil {
		log.Printf("Error querying search results for %q: %v", queryParam, err)
		c.JSON(500, gin.H{"error": "Failed to perform search in database."})
		return
	}
	defer rows.Close() // Ensure database rows are closed

	var files []Metadata // Slice to store search results
	for rows.Next() {
		m := Metadata{}
		if err := rows.Scan(&m.FileName, &m.Path, &m.Folder); err != nil {
			log.Printf("Error scanning search result row from DB: %v", err)
			continue
		}
		files = append(files, m)
	}
	if err := rows.Err(); err != nil { // Check for errors during row iteration
        log.Printf("Error during search result row iteration: %v", err)
    }
	log.Printf("Found %d search results for query %q.", len(files), queryParam)

	c.JSON(200, gin.H{"results": files}) // Return search results as a JSON object
}