# 打包（Windows）：生成图标 → mygo build，产物在 dist\windows-amd64\
#
#   powershell -ExecutionPolicy Bypass -File .\打包.ps1 [mygo build 的参数...]
#
#   DSH Desktop.exe                         GUI 程序，带图标和版本信息，没有控制台窗口
#   DSH Desktop Setup <版本> amd64.exe      NSIS 安装包：装到 %LOCALAPPDATA%\Programs，不要管理员权限，
#                                           建开始菜单快捷方式，注册 dsh:// 链接
#
# 版本号在 mygo.json 里改。没装 NSIS 时 mygo 第一次会自己下载到 %LOCALAPPDATA%\mygo。
# 本文件要存成带 BOM 的 UTF-8：Windows PowerShell 5.1 不认无 BOM 的 UTF-8，中文会乱码。

$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

go run ./tools/genicon
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
go tool mygo build @args
exit $LASTEXITCODE
