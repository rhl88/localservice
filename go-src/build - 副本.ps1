$ProjectDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$BinDir = Join-Path (Split-Path -Parent $ProjectDir) "Bin"

Write-Host "========================================" -ForegroundColor Cyan
Write-Host " CmsPro LocalService ±‡“ÎΩ≈±æ" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

# Windows amd64
Write-Host "[1/3] ±‡“Î Windows amd64 ..." -ForegroundColor Yellow
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$out = Join-Path "$BinDir\windows" "cmspro-local-service.exe"
go build -o $out (Join-Path $ProjectDir "cmd\service\main_windows.go")
if ($LASTEXITCODE -ne 0) {
    Write-Host "[!] Windows ±‡“Î ß∞‹£¨¥ÌŒÛ¬Î: $LASTEXITCODE" -ForegroundColor Red
} else {
    Write-Host "[OK] Windows: $out" -ForegroundColor Green
}

# Linux amd64
Write-Host "[2/3] ±‡“Î Linux amd64 ..." -ForegroundColor Yellow
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$out = Join-Path "$BinDir\linux" "cmspro-local-service"
go build -o $out (Join-Path $ProjectDir "cmd\service\main_linux.go")
if ($LASTEXITCODE -ne 0) {
    Write-Host "[!] Linux amd64 ±‡“Î ß∞‹£¨¥ÌŒÛ¬Î: $LASTEXITCODE" -ForegroundColor Red
} else {
    Write-Host "[OK] Linux: $out" -ForegroundColor Green
}

# Linux arm64
Write-Host "[3/3] ±‡“Î Linux arm64 ..." -ForegroundColor Yellow
$env:GOOS = "linux"
$env:GOARCH = "arm64"
$out = Join-Path "$BinDir\linux" "cmspro-local-service-arm64"
go build -o $out (Join-Path $ProjectDir "cmd\service\main_linux.go")
if ($LASTEXITCODE -ne 0) {
    Write-Host "[!] Linux arm64 ±‡“Î ß∞‹£¨¥ÌŒÛ¬Î: $LASTEXITCODE" -ForegroundColor Red
} else {
    Write-Host "[OK] Linux arm64: $out" -ForegroundColor Green
}

Write-Host ""
Write-Host "========================================" -ForegroundColor Cyan
Write-Host " ±‡“ÎÕÍ≥…" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan