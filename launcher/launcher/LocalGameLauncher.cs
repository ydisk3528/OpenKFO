using System;
using System.Diagnostics;
using System.IO;
using System.Text;
using System.Threading;
using System.Windows.Forms;

internal static class LocalGameLauncher
{
    [STAThread]
    private static int Main()
    {
        bool first;
        using (var mutex = new Mutex(true, @"Local\KungfuKidLocalGameLauncher", out first))
        {
            if (!first) return 0;
            try
            {
                string folder = AppDomain.CurrentDomain.BaseDirectory;
                string root = File.Exists(Path.Combine(folder, "tools", "Start-OneClick.ps1"))
                    ? folder : Path.Combine(folder, "OpenKFO");
                string script = Path.Combine(root, "tools", "Start-OneClick.ps1");
                if (!File.Exists(script))
                    throw new FileNotFoundException("找不到游戏启动文件。请把登录器放在游戏目录或 OpenKFO 目录中，不要单独移动 EXE。", script);
                string shell = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.System), @"WindowsPowerShell\v1.0\powershell.exe");
                var info = new ProcessStartInfo(shell, "-NoProfile -NonInteractive -ExecutionPolicy Bypass -File \"" + script + "\"")
                {
                    WorkingDirectory = root,
                    UseShellExecute = false,
                    CreateNoWindow = true
                };
                using (var process = new Process { StartInfo = info })
                {
                    process.Start();
                    if (!process.WaitForExit(90000))
                    {
                        process.Kill();
                        throw new TimeoutException("启动超时，请查看 runtime-local 下的 oneclick 日志。");
                    }
                    string logPath = Path.Combine(root, @"runtime-local\oneclick-launcher.log");
                    if (process.ExitCode != 0)
                        throw new Exception("游戏启动失败。\n\n" + (File.Exists(logPath) ? File.ReadAllText(logPath) : "未生成启动日志。") + "\n日志：" + logPath);
                }
                return 0;
            }
            catch (Exception error)
            {
                MessageBox.Show(error.Message, "启动器提示", MessageBoxButtons.OK, MessageBoxIcon.Error);
                return 1;
            }
        }
    }
}
