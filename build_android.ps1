$env:JAVA_HOME = 'C:\Program Files\Android\Android Studio\jbr'
$env:ANDROID_HOME = 'C:\Users\Yohanan.H\AppData\Local\Android\Sdk'
$env:ANDROID_NDK_HOME = 'C:\Users\Yohanan.H\AppData\Local\Android\Sdk\ndk\29.0.14206865'

Set-Location 'C:\Users\Yohanan.H\source\repos\saatools\saatool-android'
Write-Host "Running gomobile bind..."
& gomobile bind -target android/arm64 -androidapi 26 -o ..\android\app\libs\saatool-android.aar .\mobile\
if ($LASTEXITCODE -ne 0) { Write-Host "gomobile failed: $LASTEXITCODE"; exit 1 }

Write-Host "Running gradlew assembleRelease..."
Set-Location 'C:\Users\Yohanan.H\source\repos\saatools\android'
$env:JAVA_HOME = 'C:\Program Files\Android\Android Studio\jbr'
& ".\gradlew.bat" assembleRelease
if ($LASTEXITCODE -ne 0) { Write-Host "gradlew failed: $LASTEXITCODE"; exit 1 }

Write-Host "Build complete!"
