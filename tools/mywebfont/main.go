package main

import (
	"flag"
	"fmt"
	"os"

	"mywebfont"
)

func main() {
	root := flag.String("root", "webfonts", "dossier racine des webfonts")
	out := flag.String("out", "webfonts.html", "fichier HTML statique a generer")
	baseURL := flag.String("base-url", "webfonts", "URL ou chemin public vers le dossier webfonts")
	title := flag.String("title", "My Webfonts", "titre de l'interface")
	flag.Parse()

	if err := mywebfont.Generate(mywebfont.Options{
		Root:    *root,
		Out:     *out,
		BaseURL: *baseURL,
		Title:   *title,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "mywebfont: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("GUI generee: %s\n", *out)
}

