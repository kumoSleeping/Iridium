# Installs ir, Iridium's program, on this Windows computer (docs/exec.md, installing). It only
# installs (or updates); the arguments given are then run as ir:
#   irm <this script> | iex                                                   installs ir
#   & ([scriptblock]::Create((irm <this script>))) start                      then: this computer's Iridium, run at login
#   & ([scriptblock]::Create((irm <this script>))) start --no-server --join <address>   then: an executor of that one
# Iridium {{RELEASE}}, from {{BASE}}, checked against that release's SHA256SUMS. In
# %LOCALAPPDATA%\Iridium (iridium.exe, ir.cmd; that folder on the user's Path), with Iridium in the
# Start menu. ASCII only (irm may read it in any code page).
& {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'
    try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch { }
    $base = '{{BASE}}'
    $release = '{{RELEASE}}'
    $origin = '{{ORIGIN}}'
    $argv = @($args)
    $a = if (@($env:PROCESSOR_ARCHITEW6432, $env:PROCESSOR_ARCHITECTURE) -contains 'ARM64') { 'arm64' } else { 'amd64' }
    $prefix = Join-Path $env:LOCALAPPDATA 'Iridium'
    $exe = Join-Path $prefix 'iridium.exe'
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ('iridium-' + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $tmp, $prefix | Out-Null
    try {
        # Fetch: one line that fills as the file comes (kumo, 2026-11-23: curl's bar shook), redrawn
        # only as the percent changes, as wide as the console lets. Blocks where the console writes
        # UTF-8 (Windows Terminal, a console set to 65001), # and - elsewhere (an old console's
        # font may have no blocks). Output not to a console (a pipe, a log): one line, no bar.
        function Fetch($name, $url, $out) {
            Add-Type -AssemblyName System.Net.Http
            $client = New-Object System.Net.Http.HttpClient
            $client.Timeout = [TimeSpan]::FromMinutes(15)
            try {
                $res = $client.GetAsync($url, [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead).Result
                if (-not $res.IsSuccessStatusCode) { throw "download failed: $url ($([int]$res.StatusCode))" }
                $total = [long]0
                if ($res.Content.Headers.ContentLength) { $total = [long]$res.Content.Headers.ContentLength }
                $mb = '{0:N1} MB' -f ($total / 1MB)
                $bar = $true
                try { if ([Console]::IsOutputRedirected) { $bar = $false } } catch { $bar = $false }
                if (-not $bar) {
                    if ($total -gt 0) { Write-Host "Downloading $name ($mb)" } else { Write-Host "Downloading $name" }
                }
                $full = '#'; $empty = '-'
                if ($env:WT_SESSION -or [Console]::OutputEncoding.CodePage -eq 65001) { $full = [string][char]0x2588; $empty = [string][char]0x2591 }
                $cols = 80
                try { $cols = $Host.UI.RawUI.WindowSize.Width } catch { }
                if ($cols -le 0) { $cols = 80 }
                $width = [Math]::Max(10, [Math]::Min(40, $cols - $name.Length - 26))
                $in = $res.Content.ReadAsStreamAsync().Result
                $file = [IO.File]::Create($out)
                try {
                    $buf = New-Object byte[] 81920
                    $have = [long]0
                    $shown = -1
                    while (($n = $in.Read($buf, 0, $buf.Length)) -gt 0) {
                        $file.Write($buf, 0, $n)
                        $have += $n
                        if ($bar) {
                            if ($total -gt 0) {
                                $pct = [int][Math]::Min(100, [Math]::Floor($have * 100 / $total))
                                if ($pct -ne $shown) {
                                    $shown = $pct
                                    $k = [int][Math]::Floor($pct * $width / 100)
                                    $line = '  {0}  {1}{2} {3,3}%  {4}' -f $name, ($full * $k), ($empty * ($width - $k)), $pct, $mb
                                    Write-Host -NoNewline ("`r" + $line)
                                }
                            } else {
                                Write-Host -NoNewline ("`r  {0}  {1:N1} MB" -f $name, ($have / 1MB))
                            }
                        }
                    }
                } finally { $file.Close(); $in.Close() }
                if ($bar) { Write-Host '' }
            } finally { $client.Dispose() }
        }
        function Get-File($name) {
            $out = Join-Path $tmp $name
            if ($name -eq 'SHA256SUMS') {
                if (Get-Command curl.exe -ErrorAction SilentlyContinue) {
                    curl.exe -fsSL -o $out ($base + $name)
                    if ($LASTEXITCODE) { throw "download failed: $base$name" }
                } else { Invoke-WebRequest -UseBasicParsing -Uri ($base + $name) -OutFile $out }
            } else { Fetch $name ($base + $name) $out | Out-Null }
            $out
        }
        $sums = Get-Content (Get-File 'SHA256SUMS')
        $archive = "iridium-windows-$a.tar.gz"
        Write-Host "Installing Iridium $release (windows/$a)"
        $file = Get-File $archive
        $want = ($sums | Where-Object { ($_ -split '\s+')[1] -in @($archive, "*$archive") } | Select-Object -First 1) -split '\s+' | Select-Object -First 1
        $got = (Get-FileHash -Algorithm SHA256 $file).Hash.ToLower()
        if (-not $want -or $got -ne $want.ToLower()) { throw "$archive does not match SHA256SUMS: not installed" }
        # Windows 10 and later have tar.exe (bsdtar).
        tar.exe -xzf $file -C $tmp ./iridium.exe
        if ($LASTEXITCODE) { throw "could not unpack $archive" }
        # A running iridium.exe cannot be replaced, only renamed.
        if (Test-Path $exe) {
            Get-ChildItem -Path $prefix -Filter 'iridium.exe.old*' -ErrorAction SilentlyContinue | Remove-Item -Force -ErrorAction SilentlyContinue
            Move-Item -Force $exe ("$exe.old." + [DateTime]::UtcNow.Ticks)
        }
        Move-Item -Force (Join-Path $tmp 'iridium.exe') $exe
    } finally {
        Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
    }
    # ir: ir.cmd beside it, that folder on the user's Path (and this session's).
    [IO.File]::WriteAllText((Join-Path $prefix 'ir.cmd'), "@`"$exe`" ir %*`r`n")
    $path = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not (($path -split ';') -contains $prefix)) {
        [Environment]::SetEnvironmentVariable('Path', ((@($path -split ';') + $prefix) | Where-Object { $_ }) -join ';', 'User')
    }
    if (-not (($env:Path -split ';') -contains $prefix)) { $env:Path = "$env:Path;$prefix" }
    # Iridium in the Start menu.
    try {
        $lnk = Join-Path ([Environment]::GetFolderPath('Programs')) 'Iridium.lnk'
        $s = (New-Object -ComObject WScript.Shell).CreateShortcut($lnk)
        $s.TargetPath = $exe
        $s.WorkingDirectory = $prefix
        # Minimised: iridium.exe is a console program (its CLI), its console let go as the app starts;
        # shown, it flashed up first.
        $s.WindowStyle = 7
        $s.Save()
    } catch { Write-Host "No Start menu entry: $_" }
    # Not exit: run through iex, that would close the person's window.
    if ($argv.Count) {
        & $exe ir @argv
        return
    }
    Write-Host "Installed: $exe"
    Write-Host 'Next:'
    Write-Host '  ir app                        Iridium (its window)'
    Write-Host '  ir start                      this computer''s Iridium (run at login)'
    if ($origin) { Write-Host "  ir start --no-server --join $origin   an executor of $origin only" }
    else { Write-Host '  ir start --no-server --join <address>   an executor of that one only' }
    Write-Host '  ir status'
} @args
