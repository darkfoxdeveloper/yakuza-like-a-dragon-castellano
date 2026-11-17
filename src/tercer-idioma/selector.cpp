#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <dinput.h>
#include <intrin.h>
#include <MinHook.h>

#include <array>
#include <cstddef>
#include <cstdint>
#include <cstring>
#include <string>
#include <string_view>

namespace {
using AppendOption = void (*)(void*, const char* const*);
using SelectOption = unsigned char (*)(void*, void*, const int*);
using SetCursor = void (*)(void*, int);
using ConfirmAudio = unsigned char (*)(void*, const int*);
using OpenAudio = HANDLE (*)(const char*, DWORD, DWORD, LPSECURITY_ATTRIBUTES, DWORD, DWORD, HANDLE);

constexpr int english = 1;
constexpr int japanese = 2;
constexpr int castellano = 3;
constexpr char castellanoLabel[] = "Castellano";

std::uintptr_t base;
std::wstring media;
std::wstring resources;
std::wstring selectionFile;
bool activeCastellano = false;
bool spanishInNativeConfirmation = false;
bool preferenceWriteFailed = false;

AppendOption originalAppend;
SetCursor originalCursor;
SelectOption originalTitleSelection;
SelectOption originalInitialSelection;
ConfirmAudio originalConfirm;
OpenAudio originalOpen;

void trace(const char* message) {
#ifdef YAKUZA_DIAGNOSTIC
    const std::wstring log = resources + L"\\prueba.log";
    HANDLE file = CreateFileW(log.c_str(), FILE_APPEND_DATA, FILE_SHARE_READ | FILE_SHARE_WRITE,
                              nullptr, OPEN_ALWAYS, FILE_ATTRIBUTE_NORMAL, nullptr);
    if (file != INVALID_HANDLE_VALUE) {
        DWORD written = 0;
        WriteFile(file, message, static_cast<DWORD>(std::strlen(message)), &written, nullptr);
        CloseHandle(file);
    }
#else
    (void)message;
#endif
}

template <typename T>
T& field(void* object, std::size_t offset) {
    return *reinterpret_cast<T*>(static_cast<std::byte*>(object) + offset);
}

std::wstring lowercasePath(std::wstring_view path) {
    std::wstring result(path);
    for (wchar_t& character : result) {
        if (character >= L'A' && character <= L'Z') {
            character += L'a' - L'A';
        }
        if (character == L'/') {
            character = L'\\';
        }
    }
    return result;
}

std::wstring dubbedPath(std::wstring_view path) {
    const std::wstring normalized = lowercasePath(path);
    const std::wstring prefix = lowercasePath(media) + L"\\data\\";
    std::wstring_view relative(normalized);
    if (relative.starts_with(L"\\\\?\\")) {
        relative.remove_prefix(4);
    }
    if (relative.starts_with(prefix)) {
        relative.remove_prefix(prefix.size());
    } else if (relative.starts_with(L"data\\")) {
        relative.remove_prefix(5);
    }
    if (relative == L"sound_en.par") {
        return resources + L"\\sound_es.par";
    }
    constexpr std::wstring_view streams = L"stream_en\\";
    if (relative.starts_with(streams)) {
        relative.remove_prefix(streams.size());
        if (relative.ends_with(L".awb") && relative.find_first_of(L"\\:") == std::wstring_view::npos) {
            return resources + L"\\stream_es\\" + std::wstring(relative);
        }
    }
    return {};
}

HANDLE openWithDub(const char* path, DWORD access, DWORD share, LPSECURITY_ATTRIBUTES security,
                   DWORD disposition, DWORD flags, HANDLE templateFile) {
    if (activeCastellano && path && disposition == OPEN_EXISTING && (access & GENERIC_WRITE) == 0) {
        const DWORD incoming = GetLastError();
        std::array<wchar_t, 1024> wide{};
        std::wstring dubbed;
        if (MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, path, -1, wide.data(), static_cast<int>(wide.size()))) {
            dubbed = dubbedPath(wide.data());
        }
        if (!dubbed.empty() && GetFileAttributesW(dubbed.c_str()) != INVALID_FILE_ATTRIBUTES) {
            std::array<char, 1024> encoded{};
            if (WideCharToMultiByte(CP_UTF8, 0, dubbed.c_str(), -1, encoded.data(), static_cast<int>(encoded.size()), nullptr, nullptr)) {
                SetLastError(incoming);
                HANDLE result = originalOpen(encoded.data(), access, share, security, disposition, flags, templateFile);
#ifdef YAKUZA_DIAGNOSTIC
                const DWORD outgoing = GetLastError();
                if (result != INVALID_HANDLE_VALUE) {
                    const char* name = std::strrchr(encoded.data(), '\\');
                    trace((std::string("Castellano: ") + (name ? name + 1 : encoded.data()) + "\n").c_str());
                }
                SetLastError(outgoing);
#endif
                return result;
            }
        }
        SetLastError(incoming);
    }
    return originalOpen(path, access, share, security, disposition, flags, templateFile);
}

bool storedCastellano() {
    HANDLE stream = CreateFileW(selectionFile.c_str(), GENERIC_READ, FILE_SHARE_READ, nullptr,
                                OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, nullptr);
    if (stream == INVALID_HANDLE_VALUE) {
        return false;
    }
    char value = 0;
    DWORD count = 0;
    bool success = ReadFile(stream, &value, 1, &count, nullptr) && count == 1 && value == '3';
    CloseHandle(stream);
    return success;
}

bool storeChoice(int choice) {
    std::wstring pending = selectionFile + L".tmp";
    HANDLE file = CreateFileW(pending.c_str(), GENERIC_WRITE, 0, nullptr, CREATE_ALWAYS,
                              FILE_ATTRIBUTE_NORMAL, nullptr);
    if (file == INVALID_HANDLE_VALUE) {
        return false;
    }
    char value = static_cast<char>('0' + choice);
    DWORD count = 0;
    bool success = WriteFile(file, &value, 1, &count, nullptr) && count == 1 && FlushFileBuffers(file);
    CloseHandle(file);
    return success && MoveFileExW(pending.c_str(), selectionFile.c_str(),
                                  MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH);
}

bool saveTransition(int choice) {
    const bool nextCastellano = (choice == castellano);
    if (nextCastellano == activeCastellano && nextCastellano == storedCastellano()) {
        return true;
    }
    if (!storeChoice(choice)) {
        preferenceWriteFailed = true;
        return false;
    }
    preferenceWriteFailed = false;
    return true;
}

void appendWithCastellano(void* menu, const char* const* label) {
    const std::uintptr_t caller = reinterpret_cast<std::uintptr_t>(_ReturnAddress()) - base;
    originalAppend(menu, label);
    if (caller == 0x16886ad || caller == 0x1e4300d) {
        const char* extra = castellanoLabel;
        if (preferenceWriteFailed) {
            extra = "Castellano (no se pudo guardar)";
        } else if (storedCastellano() != activeCastellano) {
            extra = "Castellano (reinicia el juego)";
        }
        originalAppend(menu, &extra);
    }
}

void highlightCurrent(void* menu, int selected) {
    const std::uintptr_t caller = reinterpret_cast<std::uintptr_t>(_ReturnAddress()) - base;
    if (activeCastellano && (caller == 0x16886b9 || caller == 0x1e43019)) {
        selected = 2;
    }
    originalCursor(menu, selected);
}

unsigned char selectInTitle(void* callback, void* context, const int* index) {
    spanishInNativeConfirmation = false;
    if (*index != 2 && (*index != 0 || (!activeCastellano && !storedCastellano()))) {
        return originalTitleSelection(callback, context, index);
    }
    void* scene = field<void*>(callback, 8);
    const int current = field<int>(scene, 0x40);
    const int choice = *index == 2 ? castellano : english;
    if (current != english) {
        spanishInNativeConfirmation = choice == castellano;
        const int englishIndex = 0;
        return originalTitleSelection(callback, context, &englishIndex);
    }
    if (saveTransition(choice)) {
        trace(choice == castellano ? "Castellano confirmado.\n" : "Idioma original confirmado.\n");
    }
    const int unchanged = 0;
    return originalTitleSelection(callback, context, &unchanged);
}

unsigned char selectAtInitialPrompt(void* callback, void* context, const int* index) {
    if (*index < 0 || *index > 2) {
        return originalInitialSelection(callback, context, index);
    }
    const int choice = *index + 1;
    if (!saveTransition(choice)) {
        return 1;
    }
    const int engineIndex = (choice == japanese) ? 1 : 0;
    return originalInitialSelection(callback, context, &engineIndex);
}

unsigned char confirmChoice(void* callback, const int* answer) {
    const bool requestedSpanish = spanishInNativeConfirmation;
    spanishInNativeConfirmation = false;
    if (*answer != 3) {
        return originalConfirm(callback, answer);
    }
    void* confirmation = field<void*>(callback, 8);
    const int selected = field<int>(confirmation, 0x30);
    const int choice = requestedSpanish && selected == english ? castellano : selected;
    if (selected < english || selected > japanese) {
        return originalConfirm(callback, answer);
    }
    if (!saveTransition(choice)) {
        const int cancel = 0;
        return originalConfirm(callback, &cancel);
    }
    const unsigned char result = originalConfirm(callback, answer);
    trace(choice == castellano ? "Castellano confirmado.\n" : "Idioma original confirmado.\n");
    return result;
}

struct ExpectedCode {
    std::uintptr_t offset;
    const char* bytes;
    std::size_t length;
};

bool correctBuild() {
    constexpr ExpectedCode expected[] = {
        {0x831560, "\x40\x53\x48\x83\xec\x40\x48\xc7\x44\x24\x30\xfe", 12},
        {0x8317c0, "\x48\x83\xc1\x30\x41\xb0\x01", 7},
        {0x1687920, "\x41\x8b\x10\x45\x33\xc9\x85\xd2\x74\x19", 10},
        {0x1e42d10, "\x41\x8b\x10\x45\x33\xc9\x85\xd2\x74\x19", 10},
        {0x1686d10, "\x40\x53\x48\x83\xec\x20\x83\x3a\x03", 9},
        {0x1d66fb8, "\x40\x53\x55\x56\x57\x41\x54\x41\x56\x41\x57", 11},
    };
    const auto* dos = reinterpret_cast<const IMAGE_DOS_HEADER*>(base);
    const auto* pe = reinterpret_cast<const IMAGE_NT_HEADERS64*>(base + dos->e_lfanew);
    for (const ExpectedCode& code : expected) {
        if (code.offset + code.length > pe->OptionalHeader.SizeOfImage ||
            std::memcmp(reinterpret_cast<const void*>(base + code.offset), code.bytes, code.length) != 0) {
            return false;
        }
    }
    return true;
}

template <typename T>
bool addHook(void* target, T replacement, T& original) {
    return MH_CreateHook(target, reinterpret_cast<void*>(replacement), reinterpret_cast<void**>(&original)) == MH_OK;
}

DWORD WINAPI initialize(void*) {
    base = reinterpret_cast<std::uintptr_t>(GetModuleHandleW(nullptr));
    std::array<wchar_t, 32768> path{};
    DWORD length = GetModuleFileNameW(nullptr, path.data(), static_cast<DWORD>(path.size()));
    if (!length || length == path.size()) {
        return 0;
    }
    const std::wstring executable(path.data(), length);
    std::size_t separator = executable.find_last_of(L"\\/");
    if (separator == std::wstring::npos || _wcsicmp(executable.c_str() + separator + 1, L"YakuzaLikeADragon.exe") != 0) {
        return 0;
    }
    media = executable.substr(0, separator);
    resources = media + L"\\data\\Doblaje-Castellano";
    selectionFile = resources + L"\\idioma.txt";
    if (!correctBuild() || GetFileAttributesW((resources + L"\\sound_es.par").c_str()) == INVALID_FILE_ATTRIBUTES) {
        trace("Versión o recursos incompatibles.\n");
        return 0;
    }
    activeCastellano = storedCastellano();
    if (MH_Initialize() != MH_OK) {
        return 0;
    }
    bool complete = addHook(reinterpret_cast<void*>(base + 0x1d66fb8), openWithDub, originalOpen) &&
                    addHook(reinterpret_cast<void*>(base + 0x831560), appendWithCastellano, originalAppend) &&
                    addHook(reinterpret_cast<void*>(base + 0x8317c0), highlightCurrent, originalCursor) &&
                    addHook(reinterpret_cast<void*>(base + 0x1687920), selectInTitle, originalTitleSelection) &&
                    addHook(reinterpret_cast<void*>(base + 0x1e42d10), selectAtInitialPrompt, originalInitialSelection) &&
                    addHook(reinterpret_cast<void*>(base + 0x1686d10), confirmChoice, originalConfirm);
    if (!complete || MH_EnableHook(MH_ALL_HOOKS) != MH_OK) {
        MH_Uninitialize();
        trace("Error al activar el selector.\n");
    } else {
        trace(activeCastellano ? "Selector activo con Castellano.\n" : "Selector activo con las voces originales.\n");
    }
    return 0;
}
} // namespace

extern "C" HRESULT WINAPI ForwardDirectInput8Create(HINSTANCE instance, DWORD version, REFIID iid,
                                                      LPVOID* output, LPUNKNOWN outer) {
    static auto native = [] {
        std::array<wchar_t, MAX_PATH> folder{};
        UINT length = GetSystemDirectoryW(folder.data(), static_cast<UINT>(folder.size()));
        if (!length || length >= folder.size()) {
            return static_cast<decltype(&DirectInput8Create)>(nullptr);
        }
        std::wstring systemDll(folder.data(), length);
        systemDll += L"\\dinput8.dll";
        HMODULE module = LoadLibraryW(systemDll.c_str());
        return reinterpret_cast<decltype(&DirectInput8Create)>(module ? GetProcAddress(module, "DirectInput8Create") : nullptr);
    }();
    return native ? native(instance, version, iid, output, outer) : E_FAIL;
}

BOOL WINAPI DllMain(HINSTANCE instance, DWORD reason, LPVOID) {
    if (reason == DLL_PROCESS_ATTACH) {
        DisableThreadLibraryCalls(instance);
        if (HANDLE thread = CreateThread(nullptr, 0, initialize, nullptr, 0, nullptr)) {
            CloseHandle(thread);
        }
    }
    return TRUE;
}
