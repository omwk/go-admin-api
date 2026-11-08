# Native job handles terminate descendants even when the PowerShell host exits.
Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
public sealed class S3SmokeJob : IDisposable {
    [StructLayout(LayoutKind.Sequential)] struct Basic {
        public long ProcessTime, JobTime;
        public uint Flags;
        public UIntPtr Min, Max;
        public uint Active;
        public UIntPtr Affinity;
        public uint Priority, Scheduling;
    }
    [StructLayout(LayoutKind.Sequential)] struct IO {
        public ulong A, B, C, D, E, F;
    }
    [StructLayout(LayoutKind.Sequential)] struct Limits {
        public Basic Basic;
        public IO IO;
        public UIntPtr A, B, C, D;
    }
    [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)]
    static extern IntPtr CreateJobObject(IntPtr attr, string name);
    [DllImport("kernel32.dll", SetLastError=true)]
    static extern bool SetInformationJobObject(IntPtr job, int type, IntPtr data, uint size);
    [DllImport("kernel32.dll", SetLastError=true)]
    static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);
    [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr value);
    IntPtr handle;
    public S3SmokeJob(IntPtr process) {
        handle = CreateJobObject(IntPtr.Zero, null);
        if (handle == IntPtr.Zero) throw new Win32Exception();
        Limits limits = new Limits();
        limits.Basic.Flags = 0x2000; // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
        int size = Marshal.SizeOf(typeof(Limits));
        IntPtr buffer = Marshal.AllocHGlobal(size);
        try {
            Marshal.StructureToPtr(limits, buffer, false);
            if (!SetInformationJobObject(handle, 9, buffer, (uint)size) ||
                !AssignProcessToJobObject(handle, process)) {
                int error = Marshal.GetLastWin32Error();
                Dispose();
                throw new Win32Exception(error);
            }
        } finally { Marshal.FreeHGlobal(buffer); }
    }
    public void Dispose() {
        if (handle != IntPtr.Zero) { CloseHandle(handle); handle = IntPtr.Zero; }
        GC.SuppressFinalize(this);
    }
    ~S3SmokeJob() { Dispose(); }
}
'@

function Start-SmokeProcess([string]$File, [string[]]$Arguments, [string]$Label) {
    $info = New-Object Diagnostics.ProcessStartInfo
    $info.FileName = $File
    $info.Arguments = (($Arguments | ForEach-Object { '"' + $_ + '"' }) -join ' ')
    $info.WorkingDirectory = $script:Root
    $info.UseShellExecute = $false
    $info.CreateNoWindow = $true
    $info.RedirectStandardOutput = $true
    $info.RedirectStandardError = $true
    $info.RedirectStandardInput = $true
    foreach ($name in @($info.EnvironmentVariables.Keys)) {
        if ($name -like 'CH_*' -or $name -like 'SMOKE_*' -or $name -eq 'MOCK_FAULT') {
            $info.EnvironmentVariables.Remove($name)
        }
    }
    foreach ($name in $script:ChildEnv.Keys) { $info.EnvironmentVariables[$name] = [string]$script:ChildEnv[$name] }
    $process = New-Object Diagnostics.Process
    $process.StartInfo = $info
    if (-not $process.Start()) { throw "cannot start $Label" }
    try { $job = New-Object S3SmokeJob -ArgumentList $process.Handle } catch {
        $process.Kill()
        $process.WaitForExit()
        $process.Dispose()
        throw "cannot own process tree for $Label"
    }
    $entry = [pscustomobject]@{ Process = $process; Job = $job; Label = $Label;
        Out = $process.StandardOutput.ReadToEndAsync(); Err = $process.StandardError.ReadToEndAsync(); Closed = $false }
    $script:Processes.Add($entry)
    return $entry
}

function Stop-SmokeProcess($Entry) {
    if ($Entry.Closed) { return }
    $Entry.Job.Dispose()
    if (-not $Entry.Process.WaitForExit(3000)) { throw "process tree did not stop: $($Entry.Label)" }
    $Entry.Closed = $true
}

function Invoke-SmokeNative([string]$File, [string[]]$Arguments, [string]$Label, [string]$InputText = '') {
    $entry = Start-SmokeProcess $File $Arguments $Label
    try {
        if ($InputText) { $entry.Process.StandardInput.Write($InputText) }
        $entry.Process.StandardInput.Close()
        if (-not $entry.Process.WaitForExit((Get-SmokeRemaining))) { throw "$Label timed out" }
        $code = $entry.Process.ExitCode
        $output = $entry.Out.GetAwaiter().GetResult()
        if ($code -ne 0) {
            $safe = ''
            if ($Label -like 'helper *') { $safe = $entry.Err.GetAwaiter().GetResult().Trim() }
            throw "$Label failed (exit $code) $safe"
        }
        return $output
    } finally { Stop-SmokeProcess $entry }
}
