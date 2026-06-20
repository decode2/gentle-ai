# Test script to verify .NET SHA256 fallback produces identical results to Get-FileHash
# This ensures the fallback path works correctly when Get-FileHash is unavailable

$testFile = "$env:TEMP\gentle-ai-hash-test.txt"
$testContent = "Test content for SHA256 verification - $(Get-Random)"

try {
    # Create test file
    $testContent | Out-File -FilePath $testFile -Encoding UTF8

    # Calculate hash using Get-FileHash (standard path)
    $standardHash = (Get-FileHash -Path $testFile -Algorithm SHA256).Hash.ToLower()

    # Calculate hash using .NET fallback
    $sha256 = [System.Security.Cryptography.SHA256]::Create()
    $fileStream = [System.IO.File]::OpenRead($testFile)
    try {
        $hashBytes = $sha256.ComputeHash($fileStream)
        $fallbackHash = [System.BitConverter]::ToString($hashBytes).Replace("-", "").ToLower()
    } finally {
        $fileStream.Close()
        $sha256.Dispose()
    }

    # Verify both methods produce identical results
    if ($standardHash -eq $fallbackHash) {
        Write-Host "PASS: .NET fallback produces identical hash to Get-FileHash" -ForegroundColor Green
        Write-Host "Hash: $standardHash"
        exit 0
    } else {
        Write-Host "FAIL: Hash mismatch between Get-FileHash and .NET fallback" -ForegroundColor Red
        Write-Host "Get-FileHash: $standardHash"
        Write-Host ".NET fallback: $fallbackHash"
        exit 1
    }
} finally {
    # Cleanup
    if (Test-Path $testFile) {
        Remove-Item -Path $testFile -Force
    }
}
