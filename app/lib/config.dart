import 'package:flutter/foundation.dart';

class AppConfig {
  /// Override at build time: flutter run --dart-define=API_BASE_URL=http://192.168.1.10:8080
  static const String _defined = String.fromEnvironment('API_BASE_URL');

  static String get defaultBaseUrl {
    if (_defined.isNotEmpty) return _defined;
    if (kIsWeb) {
      final host = Uri.base.host.isEmpty ? 'localhost' : Uri.base.host;
      return 'http://$host:8080';
    }
    // The Android emulator reaches the host machine via 10.0.2.2.
    if (defaultTargetPlatform == TargetPlatform.android) return 'http://10.0.2.2:8080';
    return 'http://localhost:8080';
  }
}
