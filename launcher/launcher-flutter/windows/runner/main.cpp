#include <flutter/dart_project.h>
#include <flutter/flutter_view_controller.h>
#include <windows.h>
#include <filesystem>
#include <fstream>
#include <iterator>

#include "flutter_window.h"
#include "utils.h"
#include "launcher_lifecycle.h"

int APIENTRY wWinMain(_In_ HINSTANCE instance, _In_opt_ HINSTANCE prev,
                      _In_ wchar_t *command_line, _In_ int show_command) {
  launcher_lifecycle::Instance single_instance;
  if (!single_instance.Acquire()) return EXIT_SUCCESS;
  // Complete a staged update before loading the Dart application.
  wchar_t executable[32768];
  if (GetModuleFileNameW(nullptr, executable, 32768)) {
    auto marker = std::filesystem::path(executable).parent_path() / L".flutter-update-pending";
    if (std::filesystem::exists(marker)) {
      std::ifstream input(marker, std::ios::binary);
      std::string value((std::istreambuf_iterator<char>(input)), std::istreambuf_iterator<char>());
      int count = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, value.data(), static_cast<int>(value.size()), nullptr, 0);
      if (count > 0) {
        std::wstring plan(count, L'\0');
        MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, value.data(), static_cast<int>(value.size()), plan.data(), count);
        // Never reuse the helper left by an earlier failed update. A complete
        // launcher replacement must also repair recovery of that old plan.
        auto helper = std::filesystem::path(plan).parent_path() /
            (L"UpdateHelper-resume-" + std::to_wstring(GetCurrentProcessId()) + L".exe");
        auto installed_helper = std::filesystem::path(executable).parent_path() / L"LauncherSupport.exe";
        if (!CopyFileW(installed_helper.c_str(), helper.c_str(), FALSE)) {
          MessageBoxW(nullptr, L"无法准备更新恢复组件。请关闭启动器，使用更新修复工具重试；原更新文件已保留。", L"更新恢复失败", MB_OK | MB_ICONERROR);
          return EXIT_FAILURE;
        }
        std::wstring command = L"\"" + helper.wstring() + L"\" --apply \"" + plan + L"\"";
        STARTUPINFOW start{}; start.cb = sizeof(start); start.dwFlags = STARTF_USESHOWWINDOW; start.wShowWindow = SW_HIDE;
        PROCESS_INFORMATION process{};
        if (CreateProcessW(helper.c_str(), command.data(), nullptr, nullptr, FALSE, CREATE_NO_WINDOW, nullptr, nullptr, &start, &process)) {
          CloseHandle(process.hThread); CloseHandle(process.hProcess); return EXIT_SUCCESS;
        }
      }
      MessageBoxW(nullptr, L"更新尚未完成，请重新解压完整启动器 ZIP。", L"启动器提示", MB_OK | MB_ICONINFORMATION);
      return EXIT_FAILURE;
    }
  }

  // Attach to console when present (e.g., 'flutter run') or create a
  // new console when running with a debugger.
  if (!::AttachConsole(ATTACH_PARENT_PROCESS) && ::IsDebuggerPresent()) {
    CreateAndAttachConsole();
  }

  // Initialize COM, so that it is available for use in the library and/or
  // plugins.
  ::CoInitializeEx(nullptr, COINIT_APARTMENTTHREADED);

  flutter::DartProject project(L"data");

  std::vector<std::string> command_line_arguments =
      GetCommandLineArguments();

  project.set_dart_entrypoint_arguments(std::move(command_line_arguments));

  FlutterWindow window(project);
  Win32Window::Point origin(10, 10);
  Win32Window::Size size(1000, 780);
  if (!window.Create(L"启动器v1.1", origin, size)) {
    return EXIT_FAILURE;
  }
  window.SetQuitOnClose(true);

  ::MSG msg;
  while (::GetMessage(&msg, nullptr, 0, 0) > 0) {
    ::TranslateMessage(&msg);
    ::DispatchMessage(&msg);
  }

  ::CoUninitialize();
  return EXIT_SUCCESS;
}
