package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed index.html
var page []byte

//go:embed ilustracion.jpg
var artwork []byte

const credit = "Doblaje hecho por DaRkFox con ayuda de IA"

var libraryPath = regexp.MustCompile(`(?m)^\s*"path"\s+"([^"]+)"`)

func findGames() []string {
	home, _ := os.UserHomeDir()
	var roots []string
	if runtime.GOOS == "windows" {
		roots = []string{filepath.Join(os.Getenv("ProgramFiles(x86)"), "Steam")}
		if out, err := exec.Command("reg.exe", "query", `HKCU\Software\Valve\Steam`, "/v", "SteamPath").Output(); err == nil {
			if _, value, ok := strings.Cut(string(out), "REG_SZ"); ok {
				roots = append(roots, strings.TrimSpace(value))
			}
		}
		for drive := 'C'; drive <= 'Z'; drive++ {
			roots = append(roots, string(drive)+`:\SteamLibrary`, string(drive)+`:\Steam`)
		}
	} else {
		roots = []string{filepath.Join(home, ".local/share/Steam"), filepath.Join(home, ".steam/steam"), filepath.Join(home, ".var/app/com.valvesoftware.Steam/.local/share/Steam")}
		for _, pattern := range []string{"/run/media/deck/*", "/run/media/deck/*/SteamLibrary", "/run/media/*/SteamLibrary", "/run/media/*/*"} {
			found, _ := filepath.Glob(pattern)
			roots = append(roots, found...)
		}
	}
	var libraries []string
	for _, root := range roots {
		libraries = append(libraries, root)
		data, err := os.ReadFile(filepath.Join(root, "steamapps", "libraryfolders.vdf"))
		if err != nil {
			continue
		}
		for _, match := range libraryPath.FindAllStringSubmatch(string(data), -1) {
			libraries = append(libraries, strings.ReplaceAll(match[1], "\\\\", "\\"))
		}
	}
	games := []string{}
	seen := make(map[string]bool)
	addGame := func(path string) {
		media, err := gameMedia(path)
		if err != nil || seen[media] {
			return
		}
		seen[media] = true
		games = append(games, filepath.Dir(filepath.Dir(media)))
	}
	installDir := regexp.MustCompile(`(?mi)^\s*"installdir"\s+"([^"]+)"`)
	for _, library := range libraries {
		steamapps := filepath.Join(library, "steamapps")
		if manifest, err := os.ReadFile(filepath.Join(steamapps, "appmanifest_1235140.acf")); err == nil {
			if match := installDir.FindStringSubmatch(string(manifest)); len(match) == 2 && filepath.Base(match[1]) == match[1] {
				addGame(filepath.Join(steamapps, "common", match[1]))
			}
		}
		addGame(filepath.Join(steamapps, "common", "Yakuza Like a Dragon"))
	}
	if runtime.GOOS == "windows" {
		for drive := 'C'; drive <= 'Z'; drive++ {
			addGame(string(drive) + ":\\Yakuza Like a Dragon")
		}
	}
	return games
}

func chooseGame() (string, error) {
	if runtime.GOOS == "windows" {
		command := `Add-Type -AssemblyName System.Windows.Forms
$dialog = New-Object System.Windows.Forms.FolderBrowserDialog
$dialog.Description = 'Selecciona la carpeta de Yakuza: Like a Dragon'
$dialog.ShowNewFolderButton = $false
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
if ($dialog.ShowDialog() -eq 'OK') { [Console]::Write($dialog.SelectedPath) }`
		out, err := exec.Command("powershell.exe", "-NoProfile", "-STA", "-Command", command).Output()
		return strings.TrimSpace(string(out)), err
	}
	if tool, err := exec.LookPath("kdialog"); err == nil {
		out, err := exec.Command(tool, "--getexistingdirectory", "", "--title", "Carpeta de Yakuza").Output()
		var cancelled *exec.ExitError
		if errors.As(err, &cancelled) && cancelled.ExitCode() == 1 {
			return "", nil
		}
		return strings.TrimSpace(string(out)), err
	}
	return "", errors.New("escribe la ruta de la carpeta en el campo de texto")
}

type uiState struct {
	Running bool   `json:"running"`
	Error   bool   `json:"error"`
	Message string `json:"message"`
}

func showInstaller(args []string) error {
	if len(args) > 2 {
		return errors.New("uso: instalador-yakuza interfaz [carpeta-del-juego] [paquete]")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	bundle := filepath.Join(filepath.Dir(exe), bundleName)
	if len(args) == 2 {
		bundle = args[1]
	}
	p, closer, bundleErr := openPatch(bundle)
	if closer != nil {
		defer closer.Close()
	}
	var token [24]byte
	if _, err := rand.Read(token[:]); err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	prefix := "/" + hex.EncodeToString(token[:]) + "/"
	origin := "http://" + listener.Addr().String()
	start := ""
	if len(args) > 0 {
		start = args[0]
	}
	games := findGames()
	if start == "" && len(games) == 1 {
		start = games[0]
	}
	message := "Selecciona la carpeta de Yakuza: Like a Dragon."
	if start != "" {
		message = "Carpeta del juego detectada."
	}
	if bundleErr != nil {
		message = "Coloca Yakuza-Castellano.zip junto al instalador."
		if !errors.Is(bundleErr, os.ErrNotExist) {
			message = "El paquete de voces no se pudo abrir. " + bundleErr.Error()
		}
	}
	var stateMu sync.Mutex
	var shutdown sync.Once
	state := uiState{Message: message, Error: bundleErr != nil}
	mux := http.NewServeMux()
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	stopped := make(chan struct{})
	sendJSON := func(w http.ResponseWriter, value any) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(value)
	}
	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		if r.Host != listener.Addr().String() {
			http.Error(w, "Dirección inválida", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		endpoint := strings.TrimPrefix(r.URL.Path, prefix)
		if r.Method == http.MethodPost {
			media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if r.Header.Get("Origin") != origin || media != "application/json" {
				http.Error(w, "Petición inválida", http.StatusForbidden)
				return
			}
		}
		switch {
		case r.Method == http.MethodGet && endpoint == "":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
			_, _ = w.Write(page)
		case r.Method == http.MethodGet && endpoint == "ilustracion.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(artwork)
		case r.Method == http.MethodGet && endpoint == "config":
			config := map[string]any{"start": start, "games": games, "credit": credit, "available": p != nil}
			if p != nil {
				config["voices"] = p.Voices
			}
			sendJSON(w, config)
		case r.Method == http.MethodGet && endpoint == "state":
			stateMu.Lock()
			defer stateMu.Unlock()
			sendJSON(w, state)
		case r.Method == http.MethodPost && endpoint == "browse":
			value, err := chooseGame()
			result := map[string]string{"path": value}
			if err != nil {
				result["error"] = err.Error()
			}
			sendJSON(w, result)
		case r.Method == http.MethodPost && endpoint == "action":
			var command struct{ Action, Game string }
			err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&command)
			if err != nil || p == nil || (command.Action != "instalar" && command.Action != "desinstalar" &&
				command.Action != "estado") || strings.TrimSpace(command.Game) == "" {
				http.Error(w, "Falta una carpeta de juego válida", http.StatusBadRequest)
				return
			}
			stateMu.Lock()
			if state.Running {
				stateMu.Unlock()
				http.Error(w, "Ya hay una operación en curso", http.StatusConflict)
				return
			}
			state = uiState{Running: true, Message: "Comprobando la instalación…"}
			sendJSON(w, state)
			stateMu.Unlock()
			go func() {
				game, err := filepath.Abs(strings.Trim(strings.TrimSpace(command.Game), "\""))
				if err == nil {
					err = p.apply(game, command.Action, func(message string) {
						stateMu.Lock()
						state.Message = message
						stateMu.Unlock()
					})
				}
				stateMu.Lock()
				defer stateMu.Unlock()
				state.Running = false
				if err != nil {
					state.Error, state.Message = true, err.Error()
				}
			}()
		case r.Method == http.MethodPost && endpoint == "exit":
			stateMu.Lock()
			defer stateMu.Unlock()
			if state.Running {
				http.Error(w, "Hay una operación en curso", http.StatusConflict)
				return
			}
			sendJSON(w, true)
			shutdown.Do(func() {
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					_ = server.Shutdown(ctx)
					close(stopped)
				}()
			})
		default:
			http.NotFound(w, r)
		}
	})
	url := origin + prefix
	fmt.Println(url)
	if os.Getenv("YAKUZA_INSTALLER_NO_BROWSER") != "1" {
		var command *exec.Cmd
		if runtime.GOOS == "windows" {
			command = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url)
		} else {
			command = exec.Command("xdg-open", url)
		}
		if err := command.Start(); err == nil {
			go func() { _ = command.Wait() }()
		}
	}
	if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-stopped
	return nil
}
