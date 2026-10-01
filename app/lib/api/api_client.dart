import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;

import 'models.dart';

/// Error returned by the API (or a network failure, with [status] 0).
class ApiException implements Exception {
  ApiException(this.status, this.code, this.message);

  final int status;
  final String code;
  final String message;

  bool get isNetwork => status == 0;

  @override
  String toString() => message;
}

/// Thin typed wrapper over the Go REST API.
class ApiClient {
  ApiClient(this.baseUrl);

  /// e.g. http://localhost:8080 (no trailing slash).
  String baseUrl;

  /// JWT for staff endpoints.
  String? authToken;

  /// Called when an authenticated request is rejected with 401.
  void Function()? onUnauthorized;

  final http.Client _http = http.Client();

  Uri _uri(String path, [Map<String, String>? query]) {
    final uri = Uri.parse('$baseUrl$path');
    return (query == null || query.isEmpty) ? uri : uri.replace(queryParameters: query);
  }

  Future<dynamic> _send(
    String method,
    String path, {
    Object? body,
    Map<String, String>? query,
    Map<String, String>? headers,
  }) async {
    final req = http.Request(method, _uri(path, query));
    req.headers['Accept'] = 'application/json';
    req.headers['Content-Type'] = 'application/json';
    final token = authToken;
    if (token != null) req.headers['Authorization'] = 'Bearer $token';
    if (headers != null) req.headers.addAll(headers);
    if (body != null) req.body = jsonEncode(body);

    http.Response res;
    try {
      final streamed = await _http.send(req).timeout(const Duration(seconds: 15));
      res = await http.Response.fromStream(streamed);
    } on TimeoutException {
      throw ApiException(0, 'timeout', 'The server did not respond in time. Please try again.');
    } catch (_) {
      throw ApiException(0, 'network', 'Cannot reach the server at $baseUrl');
    }

    dynamic data;
    if (res.bodyBytes.isNotEmpty) {
      try {
        data = jsonDecode(utf8.decode(res.bodyBytes));
      } catch (_) {
        data = null;
      }
    }
    if (res.statusCode >= 200 && res.statusCode < 300) return data;

    var code = 'http_${res.statusCode}';
    var message = 'Request failed (${res.statusCode})';
    if (data is Map && data['error'] is Map) {
      final err = data['error'] as Map;
      code = (err['code'] as String?) ?? code;
      message = (err['message'] as String?) ?? message;
    }
    if (res.statusCode == 401 && token != null) onUnauthorized?.call();
    throw ApiException(res.statusCode, code, message);
  }

  Map<String, dynamic> _map(dynamic v) => (v as Map).cast<String, dynamic>();

  List<Map<String, dynamic>> _list(dynamic v) =>
      (v as List? ?? const []).map((e) => (e as Map).cast<String, dynamic>()).toList();

  // ---------------------------------------------------------------- public

  Future<bool> health() async {
    try {
      final d = await _send('GET', '/api/health');
      return d is Map && d['status'] == 'ok';
    } catch (_) {
      return false;
    }
  }

  Future<List<Department>> departments() async =>
      _list(await _send('GET', '/api/departments')).map(Department.fromJson).toList();

  Future<TokenStatus> createToken({
    required int departmentId,
    required String visitorName,
    String visitorPhone = '',
    bool isPriority = false,
    String priorityReason = '',
  }) async {
    final d = await _send('POST', '/api/tokens', body: {
      'department_id': departmentId,
      'visitor_name': visitorName,
      'visitor_phone': visitorPhone,
      'is_priority': isPriority,
      'priority_reason': priorityReason,
    });
    return TokenStatus.fromJson(_map(d));
  }

  Future<TokenStatus> tokenStatus(String id) async =>
      TokenStatus.fromJson(_map(await _send('GET', '/api/tokens/$id')));

  Future<TokenStatus> cancelToken(String id, String secret) async => TokenStatus.fromJson(
      _map(await _send('POST', '/api/tokens/$id/cancel', body: {'access_secret': secret})));

  // ------------------------------------------------------------------ auth

  Future<LoginResult> login(String email, String password) async => LoginResult.fromJson(
      _map(await _send('POST', '/api/auth/login', body: {'email': email, 'password': password})));

  Future<StaffUser> me() async => StaffUser.fromJson(_map(await _send('GET', '/api/auth/me')));

  // ----------------------------------------------------------------- staff

  Future<QueueView> queue(int departmentId) async =>
      QueueView.fromJson(_map(await _send('GET', '/api/staff/departments/$departmentId/queue')));

  Future<QueueToken> callNext(int departmentId) async => QueueToken.fromJson(
      _map(await _send('POST', '/api/staff/departments/$departmentId/call-next')));

  Future<Department> setPaused(int departmentId, bool paused, {String reason = ''}) async =>
      Department.fromJson(_map(await _send(
        'POST',
        '/api/staff/departments/$departmentId/${paused ? 'pause' : 'resume'}',
        body: {'reason': reason},
      )));

  Future<QueueToken> complete(String tokenId) async =>
      QueueToken.fromJson(_map(await _send('POST', '/api/staff/tokens/$tokenId/complete')));

  Future<NoShowResult> noShow(String tokenId) async =>
      NoShowResult.fromJson(_map(await _send('POST', '/api/staff/tokens/$tokenId/no-show')));

  Future<QueueToken> transfer(String tokenId, int toDepartmentId, {String note = ''}) async =>
      QueueToken.fromJson(_map(await _send('POST', '/api/staff/tokens/$tokenId/transfer',
          body: {'department_id': toDepartmentId, 'note': note})));

  Future<QueueToken> setPriority(String tokenId, bool priority, {String reason = ''}) async =>
      QueueToken.fromJson(_map(await _send('POST', '/api/staff/tokens/$tokenId/priority',
          body: {'is_priority': priority, 'reason': reason})));

  Future<QueueToken> staffCancel(String tokenId, {String reason = ''}) async => QueueToken.fromJson(
      _map(await _send('POST', '/api/staff/tokens/$tokenId/cancel', body: {'reason': reason})));

  Future<TokenStatus> staffIssueToken({
    required int departmentId,
    required String visitorName,
    String visitorPhone = '',
    bool isPriority = false,
    String priorityReason = '',
  }) async {
    final d = await _send('POST', '/api/staff/tokens', body: {
      'department_id': departmentId,
      'visitor_name': visitorName,
      'visitor_phone': visitorPhone,
      'is_priority': isPriority,
      'priority_reason': priorityReason,
    });
    return TokenStatus.fromJson(_map(d));
  }

  Future<DashboardData> dashboard({int? departmentId}) async => DashboardData.fromJson(_map(
      await _send('GET', '/api/staff/dashboard',
          query: departmentId == null ? null : {'department_id': '$departmentId'})));

  // ----------------------------------------------------------------- admin

  Future<List<StaffUser>> users() async =>
      _list(await _send('GET', '/api/admin/users')).map(StaffUser.fromJson).toList();

  Future<void> createUser({
    required String name,
    required String email,
    required String password,
    required String role,
    int? departmentId,
  }) async {
    await _send('POST', '/api/admin/users', body: {
      'name': name,
      'email': email,
      'password': password,
      'role': role,
      'department_id': departmentId,
    });
  }

  Future<void> setUserActive(int id, bool active) async {
    await _send('PATCH', '/api/admin/users/$id', body: {'is_active': active});
  }

  Future<Department> updateDepartment(int id, {int? avgServiceMinutes, String? name, String? description}) async {
    final body = <String, dynamic>{};
    if (avgServiceMinutes != null) body['avg_service_minutes'] = avgServiceMinutes;
    if (name != null) body['name'] = name;
    if (description != null) body['description'] = description;
    return Department.fromJson(_map(await _send('PATCH', '/api/admin/departments/$id', body: body)));
  }
}
