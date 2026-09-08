package sites

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	maxUploadBytes  = 200 << 20 // 200 Mo (archive compressée)
	maxExtractBytes = 1 << 30   // 1 Go décompressé (protection zip bomb)
)

var errArchiveTooLarge = errors.New("archive décompressée trop volumineuse (limite 1 Go)")

// ---------- Extraction : zip OU tar.gz, détecté aux magic bytes ----------

func extractArchive(data []byte, dest string) error {
	budget := int64(maxExtractBytes)
	switch {
	case len(data) > 3 && data[0] == 'P' && data[1] == 'K': // zip
		return extractZip(data, dest, &budget)
	case len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b: // gzip → tar.gz
		return extractTarGz(bytes.NewReader(data), dest, &budget)
	default:
		return errors.New("format non reconnu : fournissez un .zip ou un .tar.gz")
	}
}

func extractZip(data []byte, dest string, budget *int64) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || isJunk(f.Name) {
			continue
		}
		target, err := safeJoin(dest, f.Name)
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeFileCapped(target, rc, budget)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(r io.Reader, dest string, budget *int64) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || isJunk(h.Name) {
			continue
		}
		target, err := safeJoin(dest, h.Name)
		if err != nil {
			return err
		}
		if err := writeFileCapped(target, tr, budget); err != nil {
			return err
		}
	}
}

func isJunk(name string) bool {
	base := path.Base(strings.ReplaceAll(name, "\\", "/"))
	return strings.HasPrefix(name, "__MACOSX") || base == ".DS_Store"
}

func writeFile(target string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	f, err := os.Create(target)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

// writeFileCapped : writeFile en décomptant un budget global d'extraction.
// On compte les octets réellement écrits (les tailles annoncées par une
// archive peuvent mentir) — le budget épuisé, l'extraction échoue.
func writeFileCapped(target string, r io.Reader, budget *int64) error {
	if *budget <= 0 {
		return errArchiveTooLarge
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	f, err := os.Create(target)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(r, *budget+1))
	*budget -= n
	if err != nil {
		return err
	}
	if *budget < 0 {
		return errArchiveTooLarge
	}
	return nil
}

// flattenSingleDir : si l'archive contenait un unique dossier racine (cas pp/),
// remonte son contenu d'un cran pour que index.html soit à la racine.
func flattenSingleDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return err
	}
	inner := filepath.Join(dir, entries[0].Name())
	tmp := dir + ".tmp"
	if err := os.Rename(inner, tmp); err != nil {
		return err
	}
	if err := os.Remove(dir); err != nil {
		return err
	}
	return os.Rename(tmp, dir)
}
