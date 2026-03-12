@echo off
REM ──────────────────────────────────────────────────────────────────────────────
REM  build-android.bat  —  Build the SaaTool Android APK
REM
REM  Prerequisites (install once):
REM    1. Android Studio (includes SDK, NDK r25+, JDK 17)
REM    2. Go Mobile:
REM         go install golang.org/x/mobile/cmd/gomobile@latest
REM         gomobile init
REM    3. Add %GOPATH%\bin and Android SDK\build-tools\<ver> to PATH
REM
REM  Usage:  build-android.bat [debug|release|both]   (default: both)
REM          both  — builds debug first, then release (default behaviour)
REM ──────────────────────────────────────────────────────────────────────────────

setlocal enabledelayedexpansion

set BUILD_TYPE=%1
if "%BUILD_TYPE%"=="" set BUILD_TYPE=both

set REPO_ROOT=%~dp0
set FRONTEND_DIR=%REPO_ROOT%saatool-wails\frontend
set ANDROID_DIR=%REPO_ROOT%android
set ANDROID_LIB_DIR=%ANDROID_DIR%\app\libs
set GO_SERVER_DIR=%REPO_ROOT%saatool-android
set EMBED_DIR=%GO_SERVER_DIR%\server\frontend\dist

echo ============================================================
echo  Step 1: Build React frontend
echo ============================================================
cd /d "%FRONTEND_DIR%"
call npm run build
if errorlevel 1 ( echo [ERROR] React build failed & exit /b 1 )

echo.
echo ============================================================
echo  Step 2: Copy React dist into Go embed directory
echo ============================================================
if exist "%EMBED_DIR%" rmdir /s /q "%EMBED_DIR%"
xcopy /e /i /q "%FRONTEND_DIR%\dist" "%EMBED_DIR%"
if errorlevel 1 ( echo [ERROR] dist copy failed & exit /b 1 )

echo.
echo ============================================================
echo  Step 3: Build Go AAR with gomobile
echo ============================================================
cd /d "%GO_SERVER_DIR%"
if not exist "%ANDROID_LIB_DIR%" mkdir "%ANDROID_LIB_DIR%"
gomobile bind -target=android -androidapi 26 ^
    -o "%ANDROID_LIB_DIR%\saatool-android.aar" ^
    ./mobile/
if errorlevel 1 ( echo [ERROR] gomobile bind failed & exit /b 1 )

echo.
echo ============================================================
echo  Step 4: Build Android APK(s)  [%BUILD_TYPE%]
echo ============================================================
cd /d "%ANDROID_DIR%"

REM Generate Gradle wrapper if not present.
REM Tip: open android/ in Android Studio once — it auto-generates gradlew.bat.
if not exist "gradlew.bat" (
    echo [INFO] gradlew.bat not found — attempting to generate via 'gradle wrapper'...
    where gradle >nul 2>&1
    if errorlevel 1 (
        echo [ERROR] gradlew.bat missing and 'gradle' not found on PATH.
        echo [ERROR] Fix: Open the android/ folder in Android Studio once, or
        echo [ERROR]      install Gradle from https://gradle.org/install/
        exit /b 1
    )
    gradle wrapper
    if errorlevel 1 ( echo [ERROR] gradle wrapper generation failed & exit /b 1 )
)

REM Always build release. Optionally also build debug.
if /i NOT "%BUILD_TYPE%"=="release" (
    echo [debug] Running assembleDebug...
    call gradlew.bat assembleDebug
    if errorlevel 1 ( echo [ERROR] Gradle assembleDebug failed & exit /b 1 )
    echo  Debug APK:   %ANDROID_DIR%\app\build\outputs\apk\debug\app-debug.apk
    echo.
)

echo [release] Running assembleRelease...
call gradlew.bat assembleRelease
if errorlevel 1 ( echo [ERROR] Gradle assembleRelease failed & exit /b 1 )
echo  Release APK: %ANDROID_DIR%\app\build\outputs\apk\release\app-release.apk

echo.
echo ============================================================
echo  Build complete!
echo ============================================================
echo  Install on connected device:
if /i NOT "%BUILD_TYPE%"=="release" (
    echo    adb install -r app\build\outputs\apk\debug\app-debug.apk
)
echo    adb install -r app\build\outputs\apk\release\app-release.apk
