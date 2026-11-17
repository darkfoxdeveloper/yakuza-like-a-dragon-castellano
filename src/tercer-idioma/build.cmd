@echo off
setlocal
for /f "usebackq delims=" %%I in (`"%ProgramFiles(x86)%\Microsoft Visual Studio\Installer\vswhere.exe" -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath`) do set "YAKUZA_VS=%%I"
if not defined YAKUZA_VS exit /b 1
call "%YAKUZA_VS%\VC\Auxiliary\Build\vcvars64.bat" >nul
if errorlevel 1 exit /b 1
set "YAKUZA_MINHOOK=%~dp0minhook"
if not exist "%YAKUZA_MINHOOK%\include\MinHook.h" exit /b 1
pushd "%~dp0"
if not exist build mkdir build
cd build
cl /nologo /W3 /O2 /MT /c /I"%YAKUZA_MINHOOK%\include" "%YAKUZA_MINHOOK%\src\buffer.c" "%YAKUZA_MINHOOK%\src\hook.c" "%YAKUZA_MINHOOK%\src\trampoline.c" "%YAKUZA_MINHOOK%\src\hde\hde64.c"
if errorlevel 1 exit /b 1
set "YAKUZA_DEBUG_FLAG="
if /i "%~1"=="diagnostico" set "YAKUZA_DEBUG_FLAG=/DYAKUZA_DIAGNOSTIC"
cl /nologo /std:c++20 /EHsc /W4 /utf-8 /O2 /MT /LD %YAKUZA_DEBUG_FLAG% /I"%YAKUZA_MINHOOK%\include" ..\selector.cpp buffer.obj hook.obj trampoline.obj hde64.obj user32.lib /link /DEF:..\selector.def /OUT:dinput8.dll
set "YAKUZA_BUILD_STATUS=%ERRORLEVEL%"
popd
exit /b %YAKUZA_BUILD_STATUS%
