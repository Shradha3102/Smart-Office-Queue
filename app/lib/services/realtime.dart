import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:flutter/foundation.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

/// A queue change pushed by the server.
class QueueEvent {
  QueueEvent({required this.action, required this.departmentIds, this.tokenId});

  /// Emitted after (re)connecting: everyone should refetch, events may have been missed.
  QueueEvent.resync()
      : action = 'resync',
        departmentIds = const [],
        tokenId = null;

  final String action;
  final List<int> departmentIds;
  final String? tokenId;

  bool get isResync => action == 'resync';

  bool affects(int departmentId) => isResync || departmentIds.contains(departmentId);
}

/// Maintains a WebSocket to the server with automatic exponential-backoff reconnects.
class RealtimeService {
  RealtimeService(this._baseUrl);

  final String Function() _baseUrl;
  final _events = StreamController<QueueEvent>.broadcast();

  /// Whether the live connection is currently up.
  final ValueNotifier<bool> connected = ValueNotifier<bool>(false);

  WebSocketChannel? _channel;
  StreamSubscription<dynamic>? _sub;
  Timer? _retryTimer;
  int _attempt = 0;
  bool _disposed = false;

  Stream<QueueEvent> get events => _events.stream;

  /// Opens (or re-opens) the connection.
  void connect() {
    if (_disposed) return;
    _retryTimer?.cancel();
    _closeCurrent();

    final base = Uri.parse(_baseUrl());
    final wsUri = base.replace(
      scheme: base.scheme == 'https' ? 'wss' : 'ws',
      path: '/ws',
    );
    try {
      final channel = WebSocketChannel.connect(wsUri);
      _channel = channel;
      _sub = channel.stream.listen(
        (msg) => _onMessage(channel, msg),
        onError: (_) => _scheduleReconnect(channel),
        onDone: () => _scheduleReconnect(channel),
        cancelOnError: true,
      );
      // Surface connection failures that happen before the first frame.
      channel.ready.catchError((_) => _scheduleReconnect(channel));
    } catch (_) {
      _scheduleReconnect(null);
    }
  }

  void _onMessage(WebSocketChannel channel, dynamic raw) {
    if (!identical(channel, _channel) || raw is! String) return;
    Map<String, dynamic> msg;
    try {
      msg = (jsonDecode(raw) as Map).cast<String, dynamic>();
    } catch (_) {
      return;
    }
    switch (msg['type']) {
      case 'hello':
        _attempt = 0;
        connected.value = true;
        _events.add(QueueEvent.resync());
        break;
      case 'queue.updated':
        final ids = (msg['department_ids'] as List? ?? const [])
            .whereType<num>()
            .map((e) => e.toInt())
            .toList();
        _events.add(QueueEvent(
          action: (msg['action'] as String?) ?? 'updated',
          departmentIds: ids,
          tokenId: msg['token_id'] as String?,
        ));
        break;
    }
  }

  void _scheduleReconnect(WebSocketChannel? channel) {
    if (_disposed) return;
    if (channel != null && !identical(channel, _channel)) return; // stale socket
    connected.value = false;
    if (_retryTimer?.isActive ?? false) return;
    final seconds = min(30, pow(2, _attempt).toInt());
    _attempt = min(_attempt + 1, 6);
    _retryTimer = Timer(Duration(seconds: seconds), connect);
  }

  void _closeCurrent() {
    _sub?.cancel();
    _sub = null;
    try {
      _channel?.sink.close();
    } catch (_) {}
    _channel = null;
  }

  /// Reconnect immediately (e.g. after the server URL changed).
  void restart() {
    _attempt = 0;
    connected.value = false;
    connect();
  }

  void dispose() {
    _disposed = true;
    _retryTimer?.cancel();
    _closeCurrent();
    _events.close();
    connected.dispose();
  }
}
