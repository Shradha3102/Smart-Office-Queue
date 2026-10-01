import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../api/api_client.dart';
import '../api/models.dart';
import '../config.dart';
import 'realtime.dart';

/// Global, app-wide services (kept deliberately simple: one instance, created in main()).
late final AppServices services;

class AppServices {
  AppServices._(this.prefs, this.api, this.realtime, this.session, this.myTokens);

  final SharedPreferences prefs;
  final ApiClient api;
  final RealtimeService realtime;
  final AuthSession session;
  final MyTokensStore myTokens;

  static const _kServerUrl = 'server_url';

  static Future<AppServices> init() async {
    final prefs = await SharedPreferences.getInstance();
    final api = ApiClient(prefs.getString(_kServerUrl) ?? AppConfig.defaultBaseUrl);
    final realtime = RealtimeService(() => api.baseUrl);
    final session = AuthSession(prefs, api);
    final myTokens = MyTokensStore(prefs);
    api.onUnauthorized = session.logout;
    session.restore();
    realtime.connect();
    return AppServices._(prefs, api, realtime, session, myTokens);
  }

  String get serverUrl => api.baseUrl;

  Future<void> setServerUrl(String url) async {
    var clean = url.trim();
    while (clean.endsWith('/')) {
      clean = clean.substring(0, clean.length - 1);
    }
    api.baseUrl = clean;
    await prefs.setString(_kServerUrl, clean);
    realtime.restart();
  }
}

/// Staff login state, persisted across restarts.
class AuthSession extends ChangeNotifier {
  AuthSession(this._prefs, this._api);

  final SharedPreferences _prefs;
  final ApiClient _api;
  static const _kToken = 'auth_token';
  static const _kUser = 'auth_user';
  static const _kExpires = 'auth_expires';

  StaffUser? _user;
  StaffUser? get user => _user;
  bool get isLoggedIn => _user != null;
  bool get isAdmin => _user?.isAdmin ?? false;

  void restore() {
    final token = _prefs.getString(_kToken);
    final userJson = _prefs.getString(_kUser);
    final expires = DateTime.tryParse(_prefs.getString(_kExpires) ?? '');
    if (token == null || userJson == null) return;
    if (expires != null && expires.isBefore(DateTime.now())) {
      _clear();
      return;
    }
    try {
      _user = StaffUser.fromJson((jsonDecode(userJson) as Map).cast<String, dynamic>());
      _api.authToken = token;
    } catch (_) {
      _clear();
    }
  }

  Future<void> login(String email, String password) async {
    final res = await _api.login(email.trim(), password);
    _api.authToken = res.token;
    _user = res.user;
    await _prefs.setString(_kToken, res.token);
    await _prefs.setString(_kUser, jsonEncode(res.user.toJson()));
    if (res.expiresAt != null) {
      await _prefs.setString(_kExpires, res.expiresAt!.toIso8601String());
    }
    notifyListeners();
  }

  void logout() {
    if (_user == null && _api.authToken == null) return;
    _clear();
    notifyListeners();
  }

  void _clear() {
    _user = null;
    _api.authToken = null;
    _prefs.remove(_kToken);
    _prefs.remove(_kUser);
    _prefs.remove(_kExpires);
  }
}

/// A token this device generated (the secret allows cancelling it).
class SavedToken {
  SavedToken({required this.id, required this.secret, required this.number, required this.createdAt});

  final String id;
  final String secret;
  final String number;
  final DateTime createdAt;

  Map<String, dynamic> toJson() =>
      {'id': id, 'secret': secret, 'number': number, 'created_at': createdAt.toIso8601String()};

  factory SavedToken.fromJson(Map<String, dynamic> j) => SavedToken(
        id: j['id'] as String,
        secret: (j['secret'] as String?) ?? '',
        number: (j['number'] as String?) ?? '',
        createdAt: DateTime.tryParse((j['created_at'] as String?) ?? '') ?? DateTime.now(),
      );
}

/// Visitor's tokens on this device, kept for 24 hours.
class MyTokensStore extends ChangeNotifier {
  MyTokensStore(this._prefs) {
    _load();
  }

  final SharedPreferences _prefs;
  static const _key = 'my_tokens';
  List<SavedToken> _tokens = [];

  List<SavedToken> get tokens => List.unmodifiable(_tokens);

  SavedToken? byId(String id) {
    for (final t in _tokens) {
      if (t.id == id) return t;
    }
    return null;
  }

  void _load() {
    try {
      final raw = _prefs.getString(_key);
      if (raw == null) return;
      final cutoff = DateTime.now().subtract(const Duration(hours: 24));
      _tokens = (jsonDecode(raw) as List)
          .map((e) => SavedToken.fromJson((e as Map).cast<String, dynamic>()))
          .where((t) => t.createdAt.isAfter(cutoff))
          .toList();
    } catch (_) {
      _tokens = [];
    }
  }

  Future<void> add(SavedToken t) async {
    _tokens = [t, ..._tokens.where((e) => e.id != t.id)];
    await _save();
  }

  Future<void> updateNumber(String id, String number) async {
    final i = _tokens.indexWhere((e) => e.id == id);
    if (i < 0 || _tokens[i].number == number) return;
    final old = _tokens[i];
    _tokens[i] = SavedToken(id: old.id, secret: old.secret, number: number, createdAt: old.createdAt);
    await _save();
  }

  Future<void> remove(String id) async {
    _tokens = _tokens.where((e) => e.id != id).toList();
    await _save();
  }

  Future<void> _save() async {
    await _prefs.setString(_key, jsonEncode(_tokens.map((e) => e.toJson()).toList()));
    notifyListeners();
  }
}
