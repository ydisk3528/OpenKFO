#include "flutter_window.h"

#include <optional>
#include <flutter/method_channel.h>
#include <flutter/standard_method_codec.h>

#include "flutter/generated_plugin_registrant.h"
#include "launcher_lifecycle.h"

FlutterWindow::FlutterWindow(const flutter::DartProject& project)
    : project_(project) {}

FlutterWindow::~FlutterWindow() {}

bool FlutterWindow::OnCreate() {
  if (!Win32Window::OnCreate()) {
    return false;
  }
  SetPropW(GetHandle(), launcher_lifecycle::WindowProperty, reinterpret_cast<HANDLE>(1));

  RECT frame = GetClientArea();

  // The size here must match the window dimensions to avoid unnecessary surface
  // creation / destruction in the startup path.
  flutter_controller_ = std::make_unique<flutter::FlutterViewController>(
      frame.right - frame.left, frame.bottom - frame.top, project_);
  // Ensure that basic setup of the controller was successful.
  if (!flutter_controller_->engine() || !flutter_controller_->view()) {
    return false;
  }
  RegisterPlugins(flutter_controller_->engine());
  flutter::MethodChannel<flutter::EncodableValue> window_channel(
      flutter_controller_->engine()->messenger(), "launcher/window",
      &flutter::StandardMethodCodec::GetInstance());
  window_channel.SetMethodCallHandler([this](const auto& call, auto result) {
    const auto hwnd = GetHandle();
    if (call.method_name() == "minimize") {
      ShowWindow(hwnd, SW_MINIMIZE);
    } else if (call.method_name() == "maximize") {
      ShowWindow(hwnd, IsZoomed(hwnd) ? SW_RESTORE : SW_MAXIMIZE);
    } else if (call.method_name() == "close") {
      PostMessage(hwnd, WM_CLOSE, 0, 0);
    } else if (call.method_name() == "drag") {
      ReleaseCapture();
      PostMessage(hwnd, WM_NCLBUTTONDOWN, HTCAPTION, 0);
    } else {
      result->NotImplemented();
      return;
    }
    result->Success(flutter::EncodableValue(IsZoomed(hwnd) != FALSE));
  });
  SetChildContent(flutter_controller_->view()->GetNativeWindow());

  flutter_controller_->engine()->SetNextFrameCallback([&]() {
    const auto hwnd = GetHandle();
    SetWindowLongPtr(hwnd, GWL_STYLE, GetWindowLongPtr(hwnd, GWL_STYLE) & ~WS_CAPTION);
    SetWindowPos(hwnd, nullptr, 0, 0, 0, 0,
                 SWP_NOMOVE | SWP_NOSIZE | SWP_NOZORDER | SWP_NOACTIVATE | SWP_FRAMECHANGED);
    this->Show();
  });

  // Flutter can complete the first frame before the "show window" callback is
  // registered. The following call ensures a frame is pending to ensure the
  // window is shown. It is a no-op if the first frame hasn't completed yet.
  flutter_controller_->ForceRedraw();

  return true;
}

void FlutterWindow::OnDestroy() {
  if (flutter_controller_) {
    flutter_controller_ = nullptr;
  }

  Win32Window::OnDestroy();
}

LRESULT
FlutterWindow::MessageHandler(HWND hwnd, UINT const message,
                              WPARAM const wparam,
                              LPARAM const lparam) noexcept {
  if (message == WM_STYLECHANGING && static_cast<int>(wparam) == GWL_STYLE) {
    reinterpret_cast<STYLESTRUCT*>(lparam)->styleNew &= ~WS_CAPTION;
    return 0;
  }
  // Let Flutter occupy the entire frame, including the native title area.
  if (message == WM_NCCALCSIZE && wparam) {
    if (IsZoomed(hwnd)) {
      MONITORINFO monitor{sizeof(MONITORINFO)};
      if (GetMonitorInfo(MonitorFromWindow(hwnd, MONITOR_DEFAULTTONEAREST), &monitor)) {
        reinterpret_cast<NCCALCSIZE_PARAMS*>(lparam)->rgrc[0] = monitor.rcWork;
      }
    }
    return 0;
  }
  if (message == launcher_lifecycle::ActivateMessage()) {
    ShowWindow(hwnd, IsIconic(hwnd) ? SW_RESTORE : SW_SHOW);
    SetForegroundWindow(hwnd);
    return 0;
  }
  if (message == WM_DESTROY) {
    RemovePropW(hwnd, launcher_lifecycle::WindowProperty);
    launcher_lifecycle::BeginShutdown();
    return Win32Window::MessageHandler(hwnd, message, wparam, lparam);
  }
  // Give Flutter, including plugins, an opportunity to handle window messages.
  if (flutter_controller_) {
    std::optional<LRESULT> result =
        flutter_controller_->HandleTopLevelWindowProc(hwnd, message, wparam,
                                                      lparam);
    if (result) {
      return *result;
    }
  }

  switch (message) {
    case WM_FONTCHANGE:
      flutter_controller_->engine()->ReloadSystemFonts();
      break;
  }

  return Win32Window::MessageHandler(hwnd, message, wparam, lparam);
}
