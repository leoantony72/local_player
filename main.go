package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
)

type Metadata struct {
	Path      string
	File_name string
	Folder    string
}

const create string = `
  CREATE TABLE IF NOT EXISTS movies (
  file_name TEXT UNIQUE NOT NULL,
  path TEXT NOT NULL,
  folder TEXT NOT NULL
  );`

var sdb *sql.DB

func main() {
	Database_init()
	data := getData()
	saveData(data)
	r := gin.Default()
	r.LoadHTMLGlob("*.hbs")
	r.Static("/public", "./")

	r.GET("/", getFolders)
	r.GET("/files/:folder", getFilesFromFolder)
	r.GET("/file/:name", getFile)

	r.Run("0.0.0.0:800")
}

func getFile(c *gin.Context) {
	file_name := c.Param("name")

	row, _ := sdb.Query("SELECT path FROM movies WHERE file_name = ?;", file_name)
	defer row.Close()
	data := Metadata{}
	for row.Next() {

		err := row.Scan(&data.Path)
		if err != nil {
			fmt.Println(err)
		}
	}

	c.JSON(200, gin.H{"path": data})
}

func getFilesFromFolder(c *gin.Context) {
	folder_name := c.Param("folder")
	folder_name = folder_name + "%"
	fmt.Println("FOLDER: ", folder_name)
	rows, _ := sdb.Query("SELECT * FROM movies WHERE folder LIKE ?", folder_name)
	defer rows.Close()
	data := []Metadata{}
	for rows.Next() {
		i := Metadata{}
		err := rows.Scan(&i.File_name, &i.Path, &i.Folder)
		if err != nil {
			fmt.Println(err)
		}
		data = append(data, i)
	}
	c.JSON(200, gin.H{"files": data})
}

type Folders struct {
	Folder string `json:"folders"`
}

func getFolders(c *gin.Context) {

	rows, err := sdb.Query("SELECT DISTINCT(folder) FROM movies;")
	if err != nil {
		fmt.Println("Error: ", err)
	}
	defer rows.Close()
	data := []Folders{}
	for rows.Next() {
		i := Folders{}
		err := rows.Scan(&i.Folder)
		if err != nil {
			fmt.Println(err)
		}
		data = append(data, i)
	}

	d, _ := json.Marshal(data)

	c.HTML(200, "index.hbs", gin.H{"folders": string(d)})
}

func saveData(data []Metadata) {
	for _, d := range data {
		_, err := sdb.Exec("INSERT INTO movies VALUES(?,?,?);", d.File_name, d.Path, d.Folder)
		if err != nil {
			continue
		}
	}
	// rows, _ := db.Query("SELECT * FROM movies;")

	// defer rows.Close()

	// newdata := []Metadata{}
	// for rows.Next() {
	// 	i := Metadata{}
	// 	err := rows.Scan(&i.File_name, &i.Path, &i.Folder)
	// 	if err != nil {
	// 		fmt.Println(err)
	// 	}
	// 	newdata = append(newdata, i)
	// }

}

func Database_init() {
	db, err := sql.Open("sqlite3", "movies.db")
	if err != nil {
		log.Fatal(err)
	}
	_, err = db.Exec(create)
	if err != nil {
		log.Fatal(err)
	}
	sdb = db
}

func getData() []Metadata {
	data := []Metadata{}
	root := "./"

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {

		if err != nil {

			fmt.Println(err)
			return nil
		}

		if !info.IsDir() && filepath.Ext(path) == ".mp4" || filepath.Ext(path) == ".mkv" {
			folder := filepath.Dir(path)     //file directory -Folder
			file_name := filepath.Base(path) // file name
			data = append(data, Metadata{Path: path, File_name: file_name, Folder: folder})

		}

		return nil
	})

	if err != nil {
		fmt.Println(err)
	}
	return data
}
