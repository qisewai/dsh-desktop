# dsh-desktop 启动脚本（Windows）
#
#   powershell -ExecutionPolicy Bypass -File .\启动.ps1 [参数...]
#
# 源码比二进制新就自动重编译（需要 Go），然后在后台启动，脚本立即返回。参数原样传给 dsh-desktop.exe。
# 编成 GUI 程序（-H=windowsgui）：普通 go build 出来的是控制台程序，一启动就带个黑框，
# 关掉黑框程序也跟着退出。代价是日志不进终端，看 ~\.dsh-desktop\logs\app.log。
# 本文件要存成带 BOM 的 UTF-8：Windows PowerShell 5.1 不认无 BOM 的 UTF-8，中文会乱码。

$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

$bin = Join-Path $PSScriptRoot 'dsh-desktop.exe'

# 没有二进制，或者源码比二进制新 → 重新编译
$needBuild = -not (Test-Path $bin)
if (-not $needBuild) {
    $built = (Get-Item $bin).LastWriteTime
    $newer = Get-ChildItem -Recurse -File -Include *.go, go.mod |
        Where-Object { $_.FullName -notlike "$PSScriptRoot\dist\*" -and $_.LastWriteTime -gt $built } |
        Select-Object -First 1
    $needBuild = $null -ne $newer
}

if ($needBuild) {
    Write-Host '正在编译 dsh-desktop ...'
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        Write-Error "找不到 go，无法编译。请先安装 Go，或直接使用已编译好的 $bin"
    }
    go build -ldflags '-H=windowsgui' -o $bin .
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    Write-Host '编译完成'
}

if ($args.Count -gt 0) {
    Start-Process -FilePath $bin -ArgumentList $args
} else {
    Start-Process -FilePath $bin
}
Write-Host "已启动，日志：$(Join-Path $HOME '.dsh-desktop\logs\app.log')"
