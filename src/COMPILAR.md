# Compilar los instaladores

La carpeta contiene la interfaz del instalador, el código Go y el
complemento que añade Castellano al selector de voces. Los instaladores
leen el mismo ZIP de voces. El ZIP se distribuye como archivo de la release.

Para compilar en Windows necesitas Go 1.25 o posterior y las herramientas
de compilación x64 de Visual Studio con un Windows SDK. Desde `src`, usa
PowerShell.

```powershell
.\tercer-idioma\build.cmd
if ($LASTEXITCODE) { exit $LASTEXITCODE }
$env:CGO_ENABLED = '0'
$env:GOARCH = 'amd64'
$env:GOOS = 'windows'
go build -trimpath -ldflags '-s -w' -o ..\Instalar-Yakuza.exe .
if ($LASTEXITCODE) { exit $LASTEXITCODE }
$env:GOOS = 'linux'
go build -trimpath -ldflags '-s -w' -o ..\Instalar-Yakuza-SteamDeck .
```

La compilación Go incorpora el complemento `dinput8.dll`. El instalador
comprueba el ejecutable y los bancos originales antes de copiar el mod.
El complemento comprueba el código del juego que utiliza para extender
el selector. Al activar o desactivar Castellano hay que reiniciar el juego
para que cargue el banco correspondiente.

## Créditos

- Doblaje hecho por DaRkFox con ayuda de IA.
- Ilustración promocional de SEGA publicada en la biblioteca de Steam.
  [Yakuza: Like a Dragon](https://store.steampowered.com/app/1235140/Yakuza_Like_a_Dragon/)
- [MinHook](https://github.com/TsudaKageyu/minhook) se incluye bajo su
  [licencia BSD](tercer-idioma/minhook/LICENSE.txt).
