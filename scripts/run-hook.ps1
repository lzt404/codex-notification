$ErrorActionPreference = "Stop"

try {
    $Utf8NoBom = [System.Text.UTF8Encoding]::new($false)
    [Console]::InputEncoding = $Utf8NoBom
    [Console]::OutputEncoding = $Utf8NoBom
    $OutputEncoding = $Utf8NoBom
} catch {
}

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectRoot = Split-Path -Parent $ScriptDir

if ($env:CODEX_NOTIFICATION_ENV) {
    $EnvFile = $env:CODEX_NOTIFICATION_ENV
} else {
    $EnvFile = Join-Path $HOME ".codex/codex-notification.env"
}

if ($env:CODEX_NOTIFICATION_BIN) {
    $Bin = $env:CODEX_NOTIFICATION_BIN
} else {
    $Bin = Join-Path $ProjectRoot "bin/codex-notification.exe"
}

function Convert-EnvValue {
    param([string]$Value)

    $TrimmedValue = $Value.Trim()
    if ($TrimmedValue.Length -ge 2) {
        $FirstChar = $TrimmedValue.Substring(0, 1)
        $LastChar = $TrimmedValue.Substring($TrimmedValue.Length - 1, 1)
        if (($FirstChar -eq '"' -and $LastChar -eq '"') -or ($FirstChar -eq "'" -and $LastChar -eq "'")) {
            return $TrimmedValue.Substring(1, $TrimmedValue.Length - 2)
        }
    }

    return $TrimmedValue
}

if (Test-Path -LiteralPath $EnvFile) {
    foreach ($Line in Get-Content -LiteralPath $EnvFile) {
        $TrimmedLine = $Line.Trim()
        if ($TrimmedLine -eq "" -or $TrimmedLine.StartsWith("#")) {
            continue
        }

        $SeparatorIndex = $TrimmedLine.IndexOf("=")
        if ($SeparatorIndex -le 0) {
            continue
        }

        $Name = $TrimmedLine.Substring(0, $SeparatorIndex).Trim()
        if ($Name -notmatch "^[A-Za-z_][A-Za-z0-9_]*$") {
            continue
        }

        $RawValue = $TrimmedLine.Substring($SeparatorIndex + 1)
        $Value = Convert-EnvValue -Value $RawValue
        [Environment]::SetEnvironmentVariable($Name, $Value, "Process")
    }
}

[Environment]::SetEnvironmentVariable("CODEX_NOTIFICATION_ENV", $EnvFile, "Process")

if (Test-Path -LiteralPath $Bin) {
    & $Bin @args
    exit $LASTEXITCODE
}

Write-Error "codex-notification: binary not found: $Bin"
exit 1
