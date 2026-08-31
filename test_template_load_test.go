package main

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"html/template"
)

//go:embed web/html/*
var htmlFS embed.FS

func main() {
	funcMap := template.FuncMap{
		"i18n": func(key string, params ...string) string { return key },
	}
	
	t := template.New("").Funcs(funcMap)
	
	err := fs.WalkDir(htmlFS, "web/html", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".html") {
			return nil
		}
		
		b, err := htmlFS.ReadFile(path)
		if err != nil {
			return err
		}
		
		name := strings.TrimPrefix(path, "web/html/")
		fmt.Printf("Loading: %s\n", name)
		_, err = t.New(name).Parse(string(b))
		if err != nil {
			fmt.Printf("  ERROR: %v\n", err)
		}
		return err
	})
	
	if err != nil {
		fmt.Printf("Walk error: %v\n", err)
	}
	
	fmt.Println("\nTemplates:")
	for _, tmpl := range t.Templates() {
		fmt.Printf("  %s\n", tmpl.Name())
	}
}
