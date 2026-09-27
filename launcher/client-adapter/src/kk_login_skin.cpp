// Preserve native authentication controls. Saved choices arrive in memory only
// from the launcher; the native login procedure still performs authentication.
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <commctrl.h>
#include <cwchar>
#include <cstring>
#include <string>

// Verified unpacked SDError build. Its title-based discovery waits forever for
// gfxz's native title. Supply the selected, verified GAMECLIENT parent through
// the SDK's existing parent slot; retain its login creation/authentication code.
enum class LoginCompatibility : ULONG_PTR { MissingSdk = 1, Unreadable, UnknownCode, InvalidParent, Applied };
static LoginCompatibility attachNativeParent(HWND window) {
    static_assert(sizeof(HWND) == sizeof(LONG), "The verified SDK is x86 only");
    constexpr size_t TitleTestRva = 0x428f, ParentCallbackRva = 0x4327;
    constexpr size_t TargetProcessRva = 0x76964, ParentWindowRva = 0x795e8;
    const BYTE titleTest[] = {0x85,0xf6,0x74,0x2b,0x68,0x00,0x01,0x00,0x00,0x8d,0x85,0xfc,0xfd,0xff};
    const BYTE callback[] = {0xff,0x77,0x08,0x8b,0x57,0x04,0x8b,0xce,0xe8,0xfc,0xfe,0xff,0xff,0x83,0xc4,0x04,0x85,0xc0,0x74,0x17};
    auto module = reinterpret_cast<BYTE*>(GetModuleHandleW(L"SDError.dll"));
    if (!module) return LoginCompatibility::MissingSdk;
    HWND parent = GetAncestor(window, GA_ROOT);
    DWORD owner = 0; GetWindowThreadProcessId(parent, &owner);
    wchar_t name[64] = {}; GetClassNameW(parent, name, 64);
    if (owner != GetCurrentProcessId() || wcscmp(name, L"GAMECLIENT") ||
        !IsWindowVisible(parent) || GetWindow(parent, GW_OWNER)) return LoginCompatibility::InvalidParent;
    BYTE actualTitle[sizeof(titleTest)], actualCallback[sizeof(callback)];
    DWORD targetProcess = 0;
    SIZE_T read = 0;
    HANDLE process = GetCurrentProcess();
    if (!ReadProcessMemory(process, module + TitleTestRva, actualTitle, sizeof(actualTitle), &read) || read != sizeof(actualTitle) ||
        !ReadProcessMemory(process, module + ParentCallbackRva, actualCallback, sizeof(actualCallback), &read) || read != sizeof(actualCallback) ||
        !ReadProcessMemory(process, module + TargetProcessRva, &targetProcess, sizeof(targetProcess), &read) || read != sizeof(targetProcess)) return LoginCompatibility::Unreadable;
    // Unknown or not-yet-unpacked SDKs are left untouched.
    if (memcmp(actualTitle, titleTest, sizeof(titleTest)) || memcmp(actualCallback, callback, sizeof(callback))) return LoginCompatibility::UnknownCode;
    if (targetProcess != owner) return LoginCompatibility::InvalidParent;
    auto slot = reinterpret_cast<volatile LONG*>(module + ParentWindowRva);
    MEMORY_BASIC_INFORMATION region = {};
    if (!VirtualQuery(module + ParentWindowRva, &region, sizeof(region)) ||
        region.State != MEM_COMMIT || region.Protect != PAGE_READWRITE) return LoginCompatibility::Unreadable;
    // Do not replace an established parent; this only completes SDK discovery.
    const LONG selected = static_cast<LONG>(reinterpret_cast<ULONG_PTR>(parent));
    const LONG previous = InterlockedCompareExchange(slot, selected, 0);
    return previous == 0 || previous == selected ? LoginCompatibility::Applied : LoginCompatibility::InvalidParent;
}

static BOOL CALLBACK cleanDialogText(HWND child, LPARAM) {
    wchar_t kind[32] = {}, value[2048] = {};
    GetClassNameW(child, kind, 32);
    if (wcscmp(kind, L"Static")) return TRUE;
    GetWindowTextW(child, value, 2048);
    std::wstring text(value); size_t at;
    while ((at = text.find(L"功夫小子")) != std::wstring::npos) text.erase(at, 4);
    if (text != value) SetWindowTextW(child, text.c_str());
    return TRUE;
}
extern "C" __declspec(dllexport) BOOL __stdcall CleanLoginDialog(HWND window) {
    DWORD pid=0; GetWindowThreadProcessId(window,&pid);
    wchar_t kind[32]={},title[256]={}; GetClassNameW(window,kind,32);
    if(pid!=GetCurrentProcessId() || wcscmp(kind,L"#32770")) return FALSE;
    GetWindowTextW(window,title,256);
    if(wcsstr(title,L"功夫小子")) SetWindowTextW(window,L"登录提示");
    EnumChildWindows(window,cleanDialogText,0);
    return TRUE;
}
static const UINT CleanDialogMessage = WM_APP + 0x3b8;
static const UINT SkinMessage = WM_APP + 0x3b7;
static const UINT_PTR SkinId = 0x4b4b534b;
static HBRUSH panelBrush, fieldBrush;
static HFONT titleFont, textFont, smallFont;
static const COLORREF Panel = RGB(23, 32, 47), Field = RGB(36, 48, 66);
static const COLORREF Ink = RGB(239, 234, 219), Muted = RGB(157, 169, 185);
static const COLORREF Gold = RGB(229, 186, 103);
static constexpr int PanelWidth = 400, PanelHeight = 350;
// Same bounded UTF-16 payload as launcher-support; never persisted to disk.
static constexpr ULONG_PTR AccountsMessage = 0x4b4b4131;
static constexpr int AccountChoicesId = 1101, MaxAccounts = 8, CredentialChars = 128;
struct SavedAccount { wchar_t account[CredentialChars], password[CredentialChars]; };
struct SavedAccounts { DWORD count; SavedAccount entries[MaxAccounts]; };
static SavedAccounts savedAccounts = {};
static void centerPanel(HWND window) {
    HWND parent = GetParent(window);
    RECT area = {};
    if (!parent || !GetClientRect(parent, &area) || area.right <= 0 || area.bottom <= 0) return;
    POINT position = { (area.right - PanelWidth) / 2, (area.bottom - PanelHeight) / 2 };
    if (!(GetWindowLongW(window, GWL_STYLE) & WS_CHILD)) ClientToScreen(parent, &position);
    RECT current = {}; GetWindowRect(window, &current);
    POINT actual = {current.left, current.top};
    if (GetWindowLongW(window, GWL_STYLE) & WS_CHILD) ScreenToClient(parent, &actual);
    if (actual.x != position.x || actual.y != position.y || current.right-current.left != PanelWidth || current.bottom-current.top != PanelHeight)
        SetWindowPos(window, nullptr, position.x, position.y, PanelWidth, PanelHeight, SWP_NOZORDER | SWP_NOACTIVATE);
}

static HFONT font(int height, int weight) {
    return CreateFontW(-height, 0, 0, 0, weight, FALSE, FALSE, FALSE,
        DEFAULT_CHARSET, OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS,
        CLEARTYPE_QUALITY, DEFAULT_PITCH, L"Microsoft YaHei UI");
}
static void resources() {
    if (panelBrush) return;
    panelBrush = CreateSolidBrush(Panel); fieldBrush = CreateSolidBrush(Field);
    titleFont = font(25, FW_BOLD);
    textFont = font(17, FW_NORMAL); smallFont = font(13, FW_NORMAL);
}
static void text(HDC dc, const wchar_t* value, RECT rect, HFONT type, COLORREF color, UINT flags = DT_LEFT | DT_VCENTER | DT_SINGLELINE) {
    auto previous = SelectObject(dc, type); SetBkMode(dc, TRANSPARENT);
    SetTextColor(dc, color); DrawTextW(dc, value, -1, &rect, flags); SelectObject(dc, previous);
}
static void fill(HDC dc, RECT rect, COLORREF color) {
    HBRUSH brush = CreateSolidBrush(color); FillRect(dc, &rect, brush); DeleteObject(brush);
}
static void paint(HWND window, HDC dc) {
    RECT client; GetClientRect(window, &client); FillRect(dc, &client, panelBrush);
    text(dc, L"账号登录", {32, 22, 368, 62}, titleFont, Ink);
    text(dc, L"账号", {32, 76, 368, 98}, smallFont, Muted);
    text(dc, L"密码", {32, 152, 368, 174}, smallFont, Muted);
    fill(dc, {32, 102, 368, 140}, Field);
    fill(dc, {32, 178, 368, 216}, Field);
}
static LRESULT CALLBACK editProcedure(HWND window, UINT message, WPARAM wparam, LPARAM lparam, UINT_PTR, DWORD_PTR) {
    if (message == WM_KEYDOWN && (wparam == VK_RETURN || wparam == VK_TAB)) {
        HWND parent = GetParent(window);
        if (wparam == VK_RETURN) {
            HWND login = GetDlgItem(parent, 1003);
            if (IsWindowEnabled(login)) PostMessageW(parent, WM_COMMAND, MAKEWPARAM(1003, BN_CLICKED), (LPARAM)login);
        } else {
            HWND next = GetNextDlgTabItem(parent, window, GetKeyState(VK_SHIFT) < 0);
            if (next) SetFocus(next);
        }
        return 0;
    }
    if (message == WM_CHAR && (wparam == VK_RETURN || wparam == VK_TAB)) return 0;
    if (message == WM_NCDESTROY) RemoveWindowSubclass(window, editProcedure, SkinId);
    return DefSubclassProc(window, message, wparam, lparam);
}
static LRESULT CALLBACK loginProcedure(HWND window, UINT message, WPARAM wparam, LPARAM lparam, UINT_PTR, DWORD_PTR) {
    if (message == WM_COPYDATA) {
        const auto packet = (const COPYDATASTRUCT*)lparam;
        if (!packet || packet->dwData != AccountsMessage || packet->cbData != sizeof(SavedAccounts) || !packet->lpData) return FALSE;
        const auto choices = (const SavedAccounts*)packet->lpData;
        if (choices->count > MaxAccounts) return FALSE;
        for (DWORD i = 0; i < choices->count; ++i)
            if (!wmemchr(choices->entries[i].account, 0, CredentialChars) || !wmemchr(choices->entries[i].password, 0, CredentialChars)) return FALSE;
        HWND combo = GetDlgItem(window, AccountChoicesId);
        if (!combo) return FALSE;
        SecureZeroMemory(&savedAccounts, sizeof(savedAccounts));
        savedAccounts = *choices;
        SendMessageW(combo, CB_RESETCONTENT, 0, 0);
        for (DWORD i = 0; i < savedAccounts.count; ++i) {
            wchar_t label[160];
            swprintf_s(label, L"%s (%lu)", savedAccounts.entries[i].account, i + 1);
            SendMessageW(combo, CB_ADDSTRING, 0, (LPARAM)label);
        }
        EnableWindow(combo, savedAccounts.count != 0);
        return TRUE;
    }
    if (message == WM_COMMAND && LOWORD(wparam) == AccountChoicesId && HIWORD(wparam) == CBN_SELCHANGE) {
        LRESULT selected = SendMessageW((HWND)lparam, CB_GETCURSEL, 0, 0);
        if (selected >= 0 && (DWORD)selected < savedAccounts.count) {
            const auto& choice = savedAccounts.entries[selected];
            SetWindowTextW(GetDlgItem(window, 1001), choice.account);
            SetWindowTextW(GetDlgItem(window, 1002), choice.password);
        }
        return 0;
    }
    if (message == WM_COMMAND && LOWORD(wparam) == 1004 && HIWORD(wparam) == BN_CLICKED) {
        PostMessageW(GetAncestor(window, GA_ROOT), WM_CLOSE, 0, 0);
        return 0;
    }
    if (message == WM_ERASEBKGND) return 1;
    if (message == WM_PAINT) {
        PAINTSTRUCT state; HDC dc = BeginPaint(window, &state); paint(window, dc); EndPaint(window, &state); return 0;
    }
    if (message == WM_PRINTCLIENT) { paint(window, (HDC)wparam); return 0; }
    if (message == WM_CTLCOLOREDIT) {
        SetBkColor((HDC)wparam, Field); SetTextColor((HDC)wparam, Ink); return (LRESULT)fieldBrush;
    }
    if (message == WM_DRAWITEM && (wparam == 1003 || wparam == 1004)) {
        auto item = (DRAWITEMSTRUCT*)lparam;
        bool primary = wparam == 1003, down = (item->itemState & ODS_SELECTED) != 0;
        fill(item->hDC, item->rcItem, primary ? (down ? RGB(198, 151, 73) : Gold) : Panel);
        text(item->hDC, primary ? L"登录" : L"退出", item->rcItem, textFont,
            (item->itemState & ODS_DISABLED) ? Muted : (primary ? RGB(28, 30, 35) : Muted), DT_CENTER | DT_VCENTER | DT_SINGLELINE);
        if (item->itemState & ODS_FOCUS) { RECT focus = item->rcItem; InflateRect(&focus, -4, -4); DrawFocusRect(item->hDC, &focus); }
        return TRUE;
    }
    if (message == WM_SHOWWINDOW && wparam) {
        centerPanel(window);
    }
    if (message == WM_NCDESTROY) {
        SecureZeroMemory(&savedAccounts, sizeof(savedAccounts));
        RemoveWindowSubclass(window, loginProcedure, SkinId);
    }
    return DefSubclassProc(window, message, wparam, lparam);
}

extern "C" __declspec(dllexport) BOOL __stdcall SkinLoginWindow(HWND window) {
    DWORD_PTR installed = 0;
    if (GetWindowSubclass(window, loginProcedure, SkinId, &installed)) { centerPanel(window); return TRUE; }
    HWND account = GetDlgItem(window, 1001), password = GetDlgItem(window, 1002);
    HWND login = GetDlgItem(window, 1003), cancel = GetDlgItem(window, 1004);
    if (!account || !password || !login || !cancel) return FALSE;
    // Only the verified native password edit is styled; retain ES_PASSWORD.
    if (!(GetWindowLongW(password, GWL_STYLE) & ES_PASSWORD)) return FALSE;
    resources();
    if (!SetWindowSubclass(window, loginProcedure, SkinId, 0)) return FALSE;
    // Pin the callback module for the native window's lifetime, including
    // after the launcher helper has been closed.
    HMODULE pinned;
    GetModuleHandleExW(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | GET_MODULE_HANDLE_EX_FLAG_PIN,
        (LPCWSTR)&SkinLoginWindow, &pinned);
    for (HWND child = GetWindow(window, GW_CHILD); child; child = GetWindow(child, GW_HWNDNEXT)) {
        wchar_t className[32]; GetClassNameW(child, className, 32);
        if (!_wcsicmp(className, L"Static")) ShowWindow(child, SW_HIDE);
    }
    SetWindowLongW(window, GWL_STYLE, (GetWindowLongW(window, GWL_STYLE) & ~(WS_CAPTION | WS_BORDER | WS_THICKFRAME)) | WS_CLIPCHILDREN);
    SetWindowLongW(window, GWL_EXSTYLE, GetWindowLongW(window, GWL_EXSTYLE) & ~(WS_EX_CLIENTEDGE | WS_EX_DLGMODALFRAME));
    centerPanel(window);
    HWND edits[] = { account, password };
    for (int index = 0; index < 2; ++index) {
        HWND edit = edits[index];
        SetWindowLongW(edit, GWL_STYLE, (GetWindowLongW(edit, GWL_STYLE) & ~WS_BORDER) | WS_TABSTOP);
        SetWindowLongW(edit, GWL_EXSTYLE, GetWindowLongW(edit, GWL_EXSTYLE) & ~WS_EX_CLIENTEDGE);
        SetWindowPos(edit, nullptr, 42, 108 + index * 76, 314, 26, SWP_NOZORDER | SWP_NOACTIVATE | SWP_FRAMECHANGED);
        SendMessageW(edit, WM_SETFONT, (WPARAM)textFont, TRUE);
        SetWindowSubclass(edit, editProcedure, SkinId, 0);
    }
    HWND buttons[] = { login, cancel };
    // Keep the original editable account control and its ID for the SDK.
    SetWindowPos(account, nullptr, 42, 108, 278, 26, SWP_NOZORDER | SWP_NOACTIVATE);
    HWND choices = CreateWindowW(L"COMBOBOX", L"", WS_CHILD | WS_VISIBLE | WS_TABSTOP | CBS_DROPDOWNLIST | WS_VSCROLL,
        334, 108, 24, 220, window, (HMENU)AccountChoicesId, GetModuleHandleW(nullptr), nullptr);
    if (choices) {
        SendMessageW(choices, WM_SETFONT, (WPARAM)textFont, FALSE);
        SendMessageW(choices, CB_SETDROPPEDWIDTH, 320, 0);
        EnableWindow(choices, FALSE);
    }
    for (int index = 0; index < 2; ++index) {
        SetWindowLongW(buttons[index], GWL_STYLE, (GetWindowLongW(buttons[index], GWL_STYLE) & ~BS_TYPEMASK) | BS_OWNERDRAW | WS_TABSTOP);
        SetWindowPos(buttons[index], nullptr, 32, 242 + index * 52, 336, index ? 30 : 44, SWP_NOZORDER | SWP_NOACTIVATE | SWP_FRAMECHANGED);
    }
    SetWindowTextW(login, L"登录"); SetWindowTextW(cancel, L"退出");
    RedrawWindow(window, nullptr, nullptr, RDW_INVALIDATE | RDW_ALLCHILDREN | RDW_FRAME);
    return TRUE;
}
static BOOL CALLBACK findLogin(HWND window, LPARAM) {
    wchar_t className[64]; GetClassNameW(window, className, 64);
    if (!wcscmp(className, L"LoginChildWndClass")) SkinLoginWindow(window);
    return TRUE;
}
extern "C" __declspec(dllexport) LRESULT CALLBACK LoginSkinHook(int code, WPARAM wparam, LPARAM lparam) {
    if (code >= 0) {
        auto message = (CWPSTRUCT*)lparam;
        if (message->message == WM_INITDIALOG || message->message == WM_SHOWWINDOW)
            PostMessageW(message->hwnd, CleanDialogMessage, 0, 0);
        if (message->message == CleanDialogMessage) CleanLoginDialog(message->hwnd);
        if (message->message == SkinMessage && message->wParam == SkinId) {
            SetPropW(message->hwnd, L"OpenKFO.LoginCompatibility", reinterpret_cast<HANDLE>(attachNativeParent(message->hwnd)));
            findLogin(message->hwnd, 0);
        }
    }
    return CallNextHookEx(nullptr, code, wparam, lparam);
}
BOOL WINAPI DllMain(HINSTANCE, DWORD, LPVOID) { return TRUE; }
