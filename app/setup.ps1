# Windows PowerShell version of setup.sh
Set-Location $PSScriptRoot
flutter create . --project-name smart_queue --org com.smartoffice --platforms=android,ios,web,windows,macos,linux
flutter pub get
Write-Host ""
Write-Host "Done. Run:  flutter run -d chrome   (or an Android device / emulator)"
