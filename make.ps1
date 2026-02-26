param(
    [Parameter(Position = 0)]
    [ValidateSet("build", "run", "test", "test-short", "lint", "tidy", "clean", "help")]
    [string]$Target = "build"
)

$ErrorActionPreference = "Stop"

$BinaryName = "gopi-pro.exe"
$BuildDir = Join-Path $PSScriptRoot "build"
$BuildOutput = Join-Path $BuildDir $BinaryName
$MainPkg = "./cmd/gopi-pro/"

function Invoke-Build {
    Write-Host "Building gopi-pro..."
    if (-not (Test-Path $BuildDir)) {
        New-Item -ItemType Directory -Path $BuildDir | Out-Null
    }
    go build -ldflags="-s -w" -o $BuildOutput $MainPkg
    Write-Host "Build done: $BuildOutput"
}

function Invoke-Run {
    Invoke-Build
    & $BuildOutput
}

function Invoke-Test {
    Write-Host "Running unit tests..."
    go test ./... -v -timeout 30s
}

function Invoke-TestShort {
    Write-Host "Running short tests..."
    go test ./... -short -timeout 10s
}

function Invoke-Lint {
    Write-Host "Running go vet..."
    go vet ./...
}

function Invoke-Tidy {
    Write-Host "Tidying dependencies..."
    go mod tidy
}

function Invoke-Clean {
    Write-Host "Cleaning build directory..."
    if (Test-Path $BuildDir) {
        Remove-Item -Path $BuildDir -Recurse -Force
    }
}

function Show-Help {
    Write-Host "Available targets:"
    Write-Host "  .\make.ps1 build      - Build binary to build\gopi-pro.exe"
    Write-Host "  .\make.ps1 run        - Build and run"
    Write-Host "  .\make.ps1 test       - Run full tests"
    Write-Host "  .\make.ps1 test-short - Run short tests"
    Write-Host "  .\make.ps1 lint       - Run go vet"
    Write-Host "  .\make.ps1 tidy       - Tidy go.mod/go.sum"
    Write-Host "  .\make.ps1 clean      - Clean build artifacts"
}

switch ($Target) {
    "build" { Invoke-Build }
    "run" { Invoke-Run }
    "test" { Invoke-Test }
    "test-short" { Invoke-TestShort }
    "lint" { Invoke-Lint }
    "tidy" { Invoke-Tidy }
    "clean" { Invoke-Clean }
    default { Show-Help }
}
