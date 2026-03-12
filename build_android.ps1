$env:JAVA_HOME        = 'C:\Program Files\Android\Android Studio\jbr'
$env:ANDROID_HOME     = 'C:\Users\Yohanan.H\AppData\Local\Android\Sdk'
$env:ANDROID_NDK_HOME = 'C:\Users\Yohanan.H\AppData\Local\Android\Sdk\ndk\29.0.14206865'

# ---------------------------------------------------------------------------
# Step 3: gomobile bind
# ---------------------------------------------------------------------------
Write-Host "=== Step 3: gomobile bind ==="
Set-Location 'C:\Users\Yohanan.H\source\repos\saatools\saatool-android'
& gomobile bind -target android/arm64 -androidapi 26 `
    -o ..\android\app\libs\saatool-android.aar `
    .\mobile\
if ($LASTEXITCODE -ne 0) { Write-Error "gomobile failed: $LASTEXITCODE"; exit 1 }

# ---------------------------------------------------------------------------
# Step 4: Gradle — always build both debug AND release
# ---------------------------------------------------------------------------
Set-Location 'C:\Users\Yohanan.H\source\repos\saatools\android'

Write-Host ""
Write-Host "=== Step 4a: assembleDebug ==="
& ".\gradlew.bat" assembleDebug
if ($LASTEXITCODE -ne 0) { Write-Error "assembleDebug failed: $LASTEXITCODE"; exit 1 }

Write-Host ""
Write-Host "=== Step 4b: assembleRelease ==="
& ".\gradlew.bat" assembleRelease
if ($LASTEXITCODE -ne 0) { Write-Error "assembleRelease failed: $LASTEXITCODE"; exit 1 }

Write-Host ""
Write-Host "=== Build complete! ==="
Write-Host "  Debug APK:   android\app\build\outputs\apk\debug\app-debug.apk"
Write-Host "  Release APK: android\app\build\outputs\apk\release\app-release.apk"
Write-Host ""
Write-Host "Install on connected device:"
Write-Host "  adb install -r app\build\outputs\apk\debug\app-debug.apk"
Write-Host "  adb install -r app\build\outputs\apk\release\app-release.apk"
