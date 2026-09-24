@echo off
setlocal
call "C:\Program Files\Microsoft Visual Studio\2022\Community\VC\Auxiliary\Build\vcvars32.bat"
if errorlevel 1 exit /b 1
cd /d "%~dp0"
rc /nologo /fo previews.res previews.rc
if errorlevel 1 exit /b 1
cl /nologo /std:c++17 /O2 /MT /EHsc /W4 /utf-8 /D_WIN32_WINNT=0x0601 main.cpp previews.res /Fe:KK_Science_Exe.exe /link user32.lib gdi32.lib advapi32.lib comctl32.lib /SUBSYSTEM:WINDOWS,6.01
exit /b %errorlevel%
