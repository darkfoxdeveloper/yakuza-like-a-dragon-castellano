package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func audioDestination(original string) string {
	if original == "sound_en.par" {
		return "sound_es.par"
	}
	return "stream_es/" + strings.TrimPrefix(original, "stream_en/")
}

func installedDirectory(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	actual, err := filepath.EvalSymlinks(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || actual != path {
		return false, errors.New("La carpeta del doblaje no es válida")
	}
	flag, err := safeFile(path, markerName)
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(flag)
	if err != nil || string(data) != marker {
		return false, errors.New("Ya hay una carpeta Doblaje-Castellano de otra instalación")
	}
	return true, nil
}

func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (p *patch) removeAudio(root string) error {
	for _, file := range p.Files {
		path, err := safeFile(root, audioDestination(file.Path))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err = removeIfPresent(path); err != nil {
			return err
		}
	}
	for _, name := range []string{"stream_es", "idioma.txt", "idioma.txt.tmp"} {
		if err := removeIfPresent(filepath.Join(root, name)); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(entries) == 1 && entries[0].Name() == markerName {
		if err := os.Remove(filepath.Join(root, markerName)); err != nil {
			return err
		}
		return os.Remove(root)
	}
	if len(entries) == 0 {
		return os.Remove(root)
	}
	return errors.New("La carpeta del doblaje contiene archivos adicionales")
}

func (p *patch) verifyDub(root string) error {
	for _, file := range p.Files {
		path, err := safeFile(root, audioDestination(file.Path))
		if err != nil {
			return err
		}
		digest, err := hashFile(path)
		if err != nil || digest != file.Patched {
			return fmt.Errorf("Archivo del doblaje ausente o modificado: %s", audioDestination(file.Path))
		}
	}
	return nil
}

func (p *patch) prepareAudio(data, target string, progress func(string)) error {
	temp, err := os.MkdirTemp(data, ".yakuza-doblaje-")
	if err != nil {
		return err
	}
	defer func() { _ = p.removeAudio(temp) }()
	if err := os.Mkdir(filepath.Join(temp, "stream_es"), 0755); err != nil {
		return err
	}
	for index, file := range p.Files {
		if index%45 == 0 || index == len(p.Files)-1 {
			progress(fmt.Sprintf("Preparando bancos castellanos · %d de %d", index+1, len(p.Files)))
		}
		original, err := safeFile(data, file.Path)
		if err != nil {
			return err
		}
		destination := filepath.Join(temp, filepath.FromSlash(audioDestination(file.Path)))
		pending, err := stage(destination, file.Patched, func(out io.Writer) error {
			source, err := os.Open(original)
			if err != nil {
				return err
			}
			defer source.Close()
			return applyDelta(p.resources[file.Resource], source, out, file.Size)
		})
		if err != nil {
			return fmt.Errorf("%s: %w", file.Path, err)
		}
		if err := os.Rename(pending, destination); err != nil {
			_ = os.Remove(pending)
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(temp, markerName), []byte(marker), 0644); err != nil {
		return err
	}
	return publishNew(temp, target)
}

func (p *patch) updateAudio(data, root string, progress func(string)) error {
	work, err := os.MkdirTemp(data, ".yakuza-actualizacion-")
	if err != nil {
		return err
	}
	next := filepath.Join(work, dubbedDirectory)
	previous := filepath.Join(work, "anterior")
	defer func() {
		_ = p.removeAudio(next)
		_ = os.Remove(work)
	}()
	if err := p.prepareAudio(data, next, progress); err != nil {
		return err
	}
	choicePath, err := safeFile(root, "idioma.txt")
	if err != nil {
		return err
	}
	choice, err := os.ReadFile(choicePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		if err := os.WriteFile(filepath.Join(next, "idioma.txt"), choice, 0644); err != nil {
			return err
		}
	}
	if gameRunning() {
		return errors.New("Cierra Yakuza y vuelve a instalar para aplicar la actualización")
	}
	if err := os.Rename(root, previous); err != nil {
		return err
	}
	if err := publishNew(next, root); err != nil {
		if owned, checkErr := installedDirectory(root); checkErr != nil {
			return errors.Join(err, checkErr)
		} else if owned {
			if cleanupErr := p.removeAudio(root); cleanupErr != nil {
				return errors.Join(err, cleanupErr)
			}
		}
		return errors.Join(err, os.Rename(previous, root))
	}
	if err := p.removeAudio(previous); err != nil {
		progress("Actualización instalada. Se conserva la carpeta anterior en " + previous)
	}
	return nil
}

func (p *patch) apply(game, action string, progress func(string)) error {
	if action != "estado" && action != "instalar" && action != "desinstalar" {
		return errors.New("Acción desconocida")
	}
	media, err := gameMedia(game)
	if err != nil {
		return err
	}
	data, err := filepath.EvalSymlinks(filepath.Join(media, "data"))
	if err != nil || !within(media, data) || data == media {
		return errors.New("No se encuentra una carpeta de datos válida")
	}
	release, err := lockGame(media)
	if err != nil {
		return err
	}
	defer release()
	if action != "estado" && gameRunning() {
		return errors.New("Cierra Yakuza por completo antes de cambiar el doblaje")
	}
	loaderPath, err := safeFile(media, loaderName)
	if err != nil {
		return err
	}
	signature := sha256.Sum256(loader)
	loaderHash := hex.EncodeToString(signature[:])
	installedHash, loadErr := hashFile(loaderPath)
	if loadErr == nil && installedHash != loaderHash {
		return errors.New("Ya existe un dinput8.dll distinto en la carpeta del juego")
	}
	if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
		return loadErr
	}
	root := filepath.Join(data, dubbedDirectory)
	installed, err := installedDirectory(root)
	if err != nil {
		return err
	}
	if action == "desinstalar" {
		if loadErr == nil {
			if err := os.Remove(loaderPath); err != nil {
				return err
			}
		}
		if installed {
			if err := p.removeAudio(root); err != nil {
				return err
			}
		}
		progress("Doblaje retirado. Puedes abrir el juego con sus voces originales.")
		return nil
	}
	progress("Verificando la versión del juego y sus bancos originales…")
	if err := p.verifyOriginals(media, data); err != nil {
		return err
	}
	var incomplete bool
	if installed {
		if err := p.verifyDub(root); err != nil {
			incomplete = true
		}
	}
	if action == "estado" {
		switch {
		case incomplete:
			progress("El doblaje necesita actualizarse. Pulsa Instalar doblaje para completar los archivos.")
		case installed && loadErr == nil:
			progress("Doblaje instalado. Elige Castellano en Ajustes → Idioma de audio.")
		case installed || loadErr == nil:
			progress("Instalación parcial. Pulsa Instalar doblaje para completarla.")
		default:
			progress("Versión compatible. El doblaje está listo para instalar.")
		}
		return nil
	}
	if incomplete {
		if err := p.updateAudio(data, root, progress); err != nil {
			return err
		}
	}
	if !installed {
		if err := p.prepareAudio(data, root, progress); err != nil {
			return err
		}
	}
	if gameRunning() {
		return errors.New("Cierra Yakuza y vuelve a ejecutar el instalador para terminar")
	}
	if loadErr != nil {
		pending, err := stage(loaderPath, loaderHash, func(out io.Writer) error {
			_, err := out.Write(loader)
			return err
		})
		if err != nil {
			return err
		}
		defer os.Remove(pending)
		if err := publishNew(pending, loaderPath); err != nil {
			return fmt.Errorf("No se pudo instalar dinput8.dll: %w", err)
		}
	}
	progress("Doblaje instalado. Elige Castellano en Ajustes → Idioma de audio y reinicia el juego.")
	return nil
}
