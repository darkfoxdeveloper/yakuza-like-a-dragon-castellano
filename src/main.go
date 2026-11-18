package main

import (
	"archive/zip"
	"bufio"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const bundleName = "Yakuza-Castellano.zip"
const gameExe = "YakuzaLikeADragon.exe"
const dubbedDirectory = "Doblaje-Castellano"
const loaderName = "dinput8.dll"
const markerName = "instalacion-yakuza.txt"
const marker = "Yakuza: Like a Dragon — tercera pista de voces\n"

//go:embed tercer-idioma/build/dinput8.dll
var loader []byte

var awbName = regexp.MustCompile("^stream_en/((?:speech|auth)_[A-Za-z0-9_]+)\\.awb$")
var digestPattern = regexp.MustCompile("^[0-9a-f]{64}$")

type soundFile struct {
	Path, Resource, Original, Patched string
	Size                              int64
}

type manifest struct {
	Format         int
	Game           string
	Executable     string
	Voices, Longer int
	Files          []soundFile
}

type patch struct {
	manifest
	resources map[string]*zip.File
}

func openPatch(path string) (*patch, io.Closer, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return nil, nil, err
	}
	p := &patch{resources: make(map[string]*zip.File, len(archive.File))}
	for _, entry := range archive.File {
		if p.resources[entry.Name] != nil {
			archive.Close()
			return nil, nil, fmt.Errorf("Recurso duplicado: %s", entry.Name)
		}
		p.resources[entry.Name] = entry
	}
	err = p.readManifest()
	if err != nil {
		archive.Close()
		return nil, nil, err
	}
	return p, archive, nil
}

func (p *patch) readManifest() error {
	entry := p.resources["manifest.json"]
	if entry == nil || entry.UncompressedSize64 > 4<<20 {
		return errors.New("El paquete de voces carece de un manifiesto válido")
	}
	reader, err := entry.Open()
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(reader, 4<<20+1))
	reader.Close()
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &p.manifest); err != nil {
		return err
	}
	if p.Format != 3 || p.Game != "yakuza-like-a-dragon" || !digestPattern.MatchString(p.Executable) || p.Voices < 1 ||
		p.Longer < 0 || p.Longer > p.Voices || len(p.Files) < 2 || len(p.Files) > 3000 {
		return errors.New("El manifiesto de Yakuza es incompatible")
	}
	paths := make(map[string]bool, len(p.Files))
	for _, file := range p.Files {
		var resource string
		switch {
		case file.Path == "sound_en.par":
			resource = "delta/sound_en.bin"
		default:
			match := awbName.FindStringSubmatch(file.Path)
			if match == nil {
				return fmt.Errorf("Ruta de audio no admitida: %s", file.Path)
			}
			resource = "delta/" + match[1] + ".bin"
		}
		entry := p.resources[resource]
		path := strings.ToLower(file.Path)
		if paths[path] || resource != file.Resource || entry == nil || entry.UncompressedSize64 == 0 ||
			entry.UncompressedSize64 > 900<<20 || file.Size < 1 || file.Size > 900<<20 ||
			!digestPattern.MatchString(file.Original) || !digestPattern.MatchString(file.Patched) ||
			file.Original == file.Patched {
			return fmt.Errorf("Recurso de audio de parche no válido: %s", file.Path)
		}
		paths[path] = true
	}
	if !paths["sound_en.par"] {
		return errors.New("Falta la actualización de las tablas de voces")
	}
	return nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	_, err = io.Copy(digest, file)
	return hex.EncodeToString(digest.Sum(nil)), err
}

func within(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	separator := string(filepath.Separator)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+separator)
}

func safeFile(root, relative string) (string, error) {
	path := filepath.Join(root, filepath.FromSlash(relative))
	actual, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	if !within(root, actual) {
		return "", fmt.Errorf("La ruta sale de la carpeta elegida: %s", relative)
	}
	info, err := os.Lstat(path)
	if err == nil && !info.Mode().IsRegular() {
		return "", fmt.Errorf("Se esperaba un archivo normal: %s", relative)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return path, nil
}

func gameMedia(path string) (string, error) {
	path = strings.Trim(strings.TrimSpace(path), "\"")
	if path == "" {
		return "", errors.New("Selecciona la carpeta del juego")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for _, candidate := range []string{filepath.Join(path, "runtime", "media"), path} {
		if info, err := os.Stat(filepath.Join(candidate, gameExe)); err == nil && info.Mode().IsRegular() {
			return filepath.EvalSymlinks(candidate)
		}
	}
	return "", errors.New("No se encuentra YakuzaLikeADragon.exe en la carpeta elegida")
}

func (p *patch) verifyOriginals(media, data string) error {
	executable, err := safeFile(media, gameExe)
	if err != nil {
		return err
	}
	signature, err := hashFile(executable)
	if err != nil {
		return err
	}
	if signature != p.Executable {
		return errors.New("El selector de tres idiomas aún no admite este ejecutable del juego")
	}
	for _, file := range p.Files {
		target, err := safeFile(data, file.Path)
		if err != nil {
			return fmt.Errorf("%s: %w", file.Path, err)
		}
		digest, err := hashFile(target)
		if err != nil {
			return fmt.Errorf("%s: %w", file.Path, err)
		}
		if digest == file.Patched {
			return errors.New("Primero restaura el audio original con el instalador de la versión anterior")
		}
		if digest != file.Original {
			return fmt.Errorf("Archivo original de audio incompatible o modificado: %s", file.Path)
		}
	}
	return nil
}

// Apply bounded copy and data commands. Read to EOF so the ZIP reader also checks its CRC.
func applyDelta(entry *zip.File, source *os.File, dest io.Writer, size int64) error {
	input, err := entry.Open()
	if err != nil {
		return err
	}
	defer input.Close()
	reader := bufio.NewReader(input)
	var header [4]byte
	if _, err = io.ReadFull(reader, header[:]); err != nil || string(header[:]) != "YKD1" {
		return errors.New("Cabecera del parche de audio incorrecta")
	}
	info, err := source.Stat()
	if err != nil {
		return err
	}
	remaining := uint64(size)
	for {
		command, err := reader.ReadByte()
		if err != nil {
			return err
		}
		if command == 'E' {
			if _, end := reader.ReadByte(); end != io.EOF || remaining != 0 {
				return errors.New("El parche de audio tiene una longitud incorrecta")
			}
			return nil
		}
		var offset, count uint64
		var from io.Reader = reader
		switch command {
		case 'C':
			if err = binary.Read(reader, binary.LittleEndian, &offset); err != nil {
				return err
			}
			if err = binary.Read(reader, binary.LittleEndian, &count); err != nil {
				return err
			}
			sourceSize := uint64(info.Size())
			if offset > sourceSize || count > sourceSize-offset {
				return errors.New("Fragmento original de audio fuera de rango")
			}
			from = io.NewSectionReader(source, int64(offset), int64(count))
		case 'D':
			if err = binary.Read(reader, binary.LittleEndian, &count); err != nil {
				return err
			}
		default:
			return errors.New("Instrucción de audio desconocida")
		}
		if count == 0 || count > remaining {
			return errors.New("Fragmento de audio con longitud incorrecta")
		}
		if _, err = io.CopyN(dest, from, int64(count)); err != nil {
			return err
		}
		remaining -= count
	}
}

func stage(target, expected string, write func(io.Writer) error) (name string, err error) {
	file, err := os.CreateTemp(filepath.Dir(target), ".yakuza-audio-*")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			file.Close()
			os.Remove(file.Name())
		}
	}()
	digest := sha256.New()
	if err = write(io.MultiWriter(file, digest)); err != nil {
		return "", err
	}
	if hex.EncodeToString(digest.Sum(nil)) != expected {
		return "", errors.New("La huella del archivo generado no coincide")
	}
	// Steam files are readable by the current user on both systems.
	if err = file.Chmod(0644); err != nil {
		return "", err
	}
	if err = file.Sync(); err != nil {
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	return file.Name(), nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return showInstaller(nil)
	}
	if args[0] == "interfaz" {
		return showInstaller(args[1:])
	}
	if len(args) != 3 {
		return errors.New("Uso: instalador [interfaz] o instalador {instalar|desinstalar|estado} CARPETA PAQUETE.zip")
	}
	p, archive, err := openPatch(args[2])
	if err != nil {
		return err
	}
	defer archive.Close()
	return p.apply(args[1], args[0], func(message string) { fmt.Println(message) })
}
