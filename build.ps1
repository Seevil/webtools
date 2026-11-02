param(
    [switch]$Run,
    [string]$GoExe,
    [switch]$Offline
)
$ErrorActionPreference = 'Stop'

if ([string]::IsNullOrWhiteSpace($GoExe)) {
    $goCommand = Get-Command -Name 'go' -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -eq $goCommand) {
        throw 'Go compiler not found in PATH. MyGo v0.6.2 requires Go 1.27.1 or newer. Install a compatible Go SDK and reopen your terminal, or pass -GoExe "<full path to go.exe>" -Run. No files were built and nothing was downloaded.'
    }
    $GoExe = $goCommand.Source
} else {
    if (-not (Test-Path -LiteralPath $GoExe -PathType Leaf)) {
        throw "Go executable does not exist: $GoExe"
    }
    $GoExe = (Resolve-Path -LiteralPath $GoExe).ProviderPath
}

$environmentNames = @('GOTOOLCHAIN', 'GOPROXY', 'CGO_ENABLED', 'GOOS', 'GOARCH', 'TEMP', 'TMP')
$originalEnvironment = @{}
foreach ($name in $environmentNames) {
    $originalEnvironment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}
Push-Location $PSScriptRoot
try {
    $projectPath = [IO.Path]::GetFullPath($PSScriptRoot).TrimEnd('\', '/')
    $tempRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\', '/')
    if ($tempRoot -eq $projectPath) {
        if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) { throw 'Set TEMP/TMP to a private directory outside the project before building.' }
        $privateTemp = Join-Path $env:LOCALAPPDATA 'Temp'
        if (-not (Test-Path -LiteralPath $privateTemp -PathType Container)) { throw 'The private system temp directory is unavailable.' }
        $env:TEMP = $privateTemp
        $env:TMP = $privateTemp
    }
    $env:GOTOOLCHAIN = 'local'
    if ($Offline) { $env:GOPROXY = 'off' }
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'

    $versionOutput = & $GoExe version
    if ($LASTEXITCODE -ne 0) { throw 'The selected Go compiler could not run.' }
    Write-Host $versionOutput
    if ($versionOutput -notmatch 'go version go(\d+)\.(\d+)(?:\.(\d+))?') {
        throw "Cannot determine Go version from: $versionOutput"
    }
    $majorVersion = [int]$Matches[1]
    $minorVersion = [int]$Matches[2]
    $patchVersion = if ($Matches[3]) { [int]$Matches[3] } else { 0 }
    if ($majorVersion -lt 1 -or ($majorVersion -eq 1 -and ($minorVersion -lt 27 -or ($minorVersion -eq 27 -and $patchVersion -lt 1)))) {
        throw 'MyGo v0.6.2 requires Go 1.27.1 or newer. Install a compatible compiler or use -GoExe to select it.'
    }
    & $GoExe mod tidy
    if ($LASTEXITCODE -ne 0) { throw 'Dependency resolution failed. Check Go version (1.27.1+) and network access, or use -Offline with a populated module cache.' }
    & $GoExe run ./cmd/iconbuild ./cmd/siteclone/assets/mark.svg ./cmd/siteclone/assets
    if ($LASTEXITCODE -ne 0) { throw 'Icon generation failed.' }
    $iconPath = Join-Path $PSScriptRoot 'cmd/siteclone/assets/app.ico'
    $resourcePath = Join-Path $PSScriptRoot 'cmd/siteclone/rsrc_windows_amd64.syso'
    if (-not $Offline) {
        & $GoExe run github.com/tc-hib/go-winres@v0.3.3 simply --icon $iconPath --arch amd64 --out (Join-Path $PSScriptRoot 'cmd/siteclone/rsrc') --manifest gui --file-description '网站克隆工具' --product-name '网站克隆工具' --original-filename 'SiteClone.exe'
        if ($LASTEXITCODE -ne 0) { throw 'Windows icon resource generation failed.' }
    } elseif (-not (Test-Path -LiteralPath $resourcePath -PathType Leaf) -or (Get-Item -LiteralPath $resourcePath).LastWriteTimeUtc -lt (Get-Item -LiteralPath $iconPath).LastWriteTimeUtc) {
        throw 'Windows icon resource is missing or outdated. Run .\build.ps1 once without -Offline to regenerate it.'
    }
    & $GoExe fmt ./...
    if ($LASTEXITCODE -ne 0) { throw 'Formatting failed.' }
    & $GoExe test ./... -count=1 -timeout=90s
    if ($LASTEXITCODE -ne 0) { throw 'Tests failed.' }
    & $GoExe vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'Static checks failed.' }
    & $GoExe build -trimpath -ldflags='-H=windowsgui -s -w' -o SiteClone.exe ./cmd/siteclone
    if ($LASTEXITCODE -ne 0) { throw 'Build failed.' }
    Write-Host 'Built SiteClone.exe. MyGo is linked into the application; dependencies may be downloaded during build.'
    if ($Run) { Start-Process -FilePath (Join-Path $PSScriptRoot 'SiteClone.exe') }
} finally {
    foreach ($name in $environmentNames) {
        [Environment]::SetEnvironmentVariable($name, $originalEnvironment[$name], 'Process')
    }
    Pop-Location
}
