param(
    [string]$Version = "1.0.1"
)

$ProjectDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$BinDir = Join-Path (Split-Path -Parent $ProjectDir) "Bin"
$LdFlags = "-X cmspro-local-service/internal/buildinfo.Version=$Version"

Write-Host "========================================" -ForegroundColor Cyan
Write-Host " CmsPro LocalService 构建脚本" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

# Windows amd64
Write-Host "[1/3] 构建 Windows amd64 ..." -ForegroundColor Yellow
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$out = Join-Path "$BinDir\windows" "cmspro-local-service.exe"
Push-Location $ProjectDir
go build -ldflags $LdFlags -o $out ./cmd/service/main_windows.go
if ($LASTEXITCODE -ne 0) {
    Write-Host "[!] Windows 构建失败，错误码: $LASTEXITCODE" -ForegroundColor Red
} else {
    Write-Host "[OK] Windows: $out" -ForegroundColor Green
}
Pop-Location

# Linux amd64
Write-Host "[2/3] 构建 Linux amd64 ..." -ForegroundColor Yellow
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$out = Join-Path "$BinDir\linux" "cmspro-local-service"
Push-Location $ProjectDir
go build -ldflags $LdFlags -o $out ./cmd/service/main_linux.go
if ($LASTEXITCODE -ne 0) {
    Write-Host "[!] Linux amd64 构建失败，错误码: $LASTEXITCODE" -ForegroundColor Red
} else {
    Write-Host "[OK] Linux: $out" -ForegroundColor Green
}
Pop-Location

# Linux arm64
Write-Host "[3/3] 构建 Linux arm64 ..." -ForegroundColor Yellow
$env:GOOS = "linux"
$env:GOARCH = "arm64"
$out = Join-Path "$BinDir\linux" "cmspro-local-service-arm64"
Push-Location $ProjectDir
go build -ldflags $LdFlags -o $out ./cmd/service/main_linux.go
if ($LASTEXITCODE -ne 0) {
    Write-Host "[!] Linux arm64 构建失败，错误码: $LASTEXITCODE" -ForegroundColor Red
} else {
    Write-Host "[OK] Linux arm64: $out" -ForegroundColor Green
}
Pop-Location

Write-Host ""
Write-Host "========================================" -ForegroundColor Cyan
Write-Host " 构建完成" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
