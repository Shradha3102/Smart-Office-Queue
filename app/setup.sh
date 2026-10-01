#!/usr/bin/env bash
# Generates the platform folders (android/ios/web/windows/macos/linux) around the
# provided lib/ code, then fetches packages. Existing files are never overwritten.
set -euo pipefail
cd "$(dirname "$0")"
flutter create . --project-name smart_queue --org com.smartoffice --platforms=android,ios,web,windows,macos,linux
flutter pub get
echo
echo "Done. Run:  flutter run -d chrome   (or an Android device / emulator)"
