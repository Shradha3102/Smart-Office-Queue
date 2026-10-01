import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../api/api_client.dart';
import '../../api/models.dart';
import '../../services/app_services.dart';
import '../../services/realtime.dart';
import '../../widgets/common.dart';

/// Live view of a single token: position, ETA, now-serving, cancel.
class TokenTrackingScreen extends StatefulWidget {
  const TokenTrackingScreen({super.key, required this.tokenId, this.justCreated = false});

  final String tokenId;
  final bool justCreated;

  @override
  State<TokenTrackingScreen> createState() => _TokenTrackingScreenState();
}

class _TokenTrackingScreenState extends State<TokenTrackingScreen> with LiveRefresh<TokenTrackingScreen> {
  TokenStatus? _status;
  String? _error;
  bool _cancelling = false;

  @override
  void initState() {
    super.initState();
    _load();
    startLive();
  }

  @override
  Duration get pollInterval => const Duration(seconds: 20);

  @override
  bool wantsEvent(QueueEvent e) {
    final s = _status;
    return s == null || e.tokenId == widget.tokenId || e.affects(s.token.departmentId);
  }

  @override
  Future<void> onLiveRefresh() => _load();

  Future<void> _load() async {
    try {
      final st = await services.api.tokenStatus(widget.tokenId);
      if (!mounted) return;
      final hadStatus = _status != null;
      final wasServing = _status?.token.isServing ?? false;
      setState(() {
        _status = st;
        _error = null;
      });
      services.myTokens.updateNumber(st.token.id, st.token.tokenNumber);
      if (hadStatus && st.token.isServing && !wasServing) {
        HapticFeedback.heavyImpact();
        SystemSound.play(SystemSoundType.alert);
      }
    } on ApiException catch (e) {
      if (!mounted) return;
      if (e.code == 'token_not_found') {
        await services.myTokens.remove(widget.tokenId);
      }
      setState(() => _error = e.message);
    }
  }

  Future<void> _cancel() async {
    final saved = services.myTokens.byId(widget.tokenId);
    if (saved == null || saved.secret.isEmpty) {
      showSnack(context, 'This token was not created on this device, so it cannot be cancelled here.', error: true);
      return;
    }
    final ok = await confirmDialog(
      context,
      title: 'Cancel token ${_status?.token.tokenNumber ?? ''}?',
      message: 'You will lose your place in the queue. This cannot be undone.',
      confirm: 'Cancel token',
      danger: true,
    );
    if (!ok || !mounted) return;
    setState(() => _cancelling = true);
    try {
      final st = await services.api.cancelToken(widget.tokenId, saved.secret);
      if (!mounted) return;
      setState(() => _status = st);
      showSnack(context, 'Your token has been cancelled');
    } catch (e) {
      if (mounted) showSnack(context, errorText(e), error: true);
    } finally {
      if (mounted) setState(() => _cancelling = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final st = _status;
    return Scaffold(
      appBar: AppBar(
        title: Text(st == null ? 'Your token' : 'Token ${st.token.tokenNumber}'),
        actions: [
          const ConnectionDot(),
          IconButton(onPressed: _load, tooltip: 'Refresh', icon: const Icon(Icons.refresh)),
        ],
      ),
      body: st == null
          ? (_error != null
              ? ErrorRetry(message: _error!, onRetry: _load)
              : const Center(child: CircularProgressIndicator()))
          : RefreshIndicator(
              onRefresh: _load,
              child: ListView(
                padding: const EdgeInsets.all(16),
                children: [MaxWidth(width: 560, child: _body(st))],
              ),
            ),
    );
  }

  Widget _body(TokenStatus st) {
    final t = st.token;
    final theme = Theme.of(context);
    final canCancel = t.isActive && (services.myTokens.byId(t.id)?.secret.isNotEmpty ?? false);

    return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
      if (widget.justCreated && t.isWaiting)
        const Padding(
          padding: EdgeInsets.only(bottom: 12),
          child: Banner2(
            icon: Icons.check_circle_rounded,
            color: Colors.green,
            text: 'Your token is confirmed. Keep this screen open - it updates automatically.',
          ),
        ),
      _TicketCard(status: st),
      const SizedBox(height: 16),
      if (t.isWaiting) ...[
        Row(children: [
          Expanded(child: _Metric(label: 'Position', value: '#${t.position ?? '-'}', icon: Icons.format_list_numbered)),
          const SizedBox(width: 10),
          Expanded(child: _Metric(label: 'People ahead', value: '${t.peopleAhead ?? '-'}', icon: Icons.people_alt_outlined)),
        ]),
        const SizedBox(height: 10),
        Row(children: [
          Expanded(child: _Metric(label: 'Estimated wait', value: formatEta(t.estimatedWaitMinutes), icon: Icons.timer_outlined)),
          const SizedBox(width: 10),
          Expanded(child: _Metric(label: 'Now serving', value: st.nowServing ?? '-', icon: Icons.campaign_outlined)),
        ]),
        const SizedBox(height: 12),
        if (st.departmentPaused)
          Banner2(
            icon: Icons.pause_circle_filled_rounded,
            color: Colors.orange,
            text: '${st.departmentName} is paused for now'
                '${st.pauseReason.isEmpty ? '' : ' (${st.pauseReason})'}. '
                'Your place in the queue is kept.',
          ),
        if (t.noShowCount > 0) ...[
          const SizedBox(height: 8),
          const Banner2(
            icon: Icons.warning_amber_rounded,
            color: Colors.deepOrange,
            text: 'You missed your call once, so your token was moved to the end of the queue. '
                'If you miss it again, the token will be cancelled automatically.',
          ),
        ],
        if (t.inPriorityLane) ...[
          const SizedBox(height: 8),
          const Banner2(
            icon: Icons.star_rounded,
            color: Colors.purple,
            text: 'Priority token: you will be called ahead of regular tokens.',
          ),
        ],
      ],
      if (t.isServing)
        Banner2(
          icon: Icons.notifications_active_rounded,
          color: Colors.green,
          text: "It's your turn! Please proceed to the ${st.departmentName} counter now.",
        ),
      if (!t.isActive)
        Banner2(
          icon: t.status == TokenStatusValue.completed ? Icons.verified_rounded : Icons.cancel_rounded,
          color: t.status == TokenStatusValue.completed ? Colors.teal : Colors.grey,
          text: t.status == TokenStatusValue.completed
              ? 'Your visit has been completed. Thank you!'
              : (t.closeReason.isEmpty ? 'This token is no longer active.' : t.closeReason),
        ),
      const SizedBox(height: 16),
      _DetailsCard(token: t),
      const SizedBox(height: 16),
      if (canCancel)
        OutlinedButton.icon(
          style: OutlinedButton.styleFrom(
            foregroundColor: theme.colorScheme.error,
            minimumSize: const Size.fromHeight(48),
          ),
          onPressed: _cancelling ? null : _cancel,
          icon: const Icon(Icons.close_rounded),
          label: Text(_cancelling ? 'Cancelling…' : 'Cancel my token'),
        ),
    ]);
  }
}

class _TicketCard extends StatelessWidget {
  const _TicketCard({required this.status});
  final TokenStatus status;

  @override
  Widget build(BuildContext context) {
    final t = status.token;
    final scheme = Theme.of(context).colorScheme;
    final serving = t.isServing;
    final bg = serving ? Colors.green.shade600 : (t.isActive ? scheme.primary : scheme.surfaceContainerHighest);
    final fg = serving || t.isActive ? Colors.white : scheme.onSurface;
    return AnimatedContainer(
      duration: const Duration(milliseconds: 400),
      padding: const EdgeInsets.symmetric(vertical: 28, horizontal: 20),
      decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(24)),
      child: Column(children: [
        Row(mainAxisAlignment: MainAxisAlignment.center, children: [
          Icon(departmentIcon(status.departmentCode), color: fg),
          const SizedBox(width: 8),
          Text(status.departmentName, style: TextStyle(color: fg, fontSize: 18, fontWeight: FontWeight.w500)),
        ]),
        const SizedBox(height: 8),
        FittedBox(
          child: Text(t.tokenNumber,
              style: TextStyle(color: fg, fontSize: 64, fontWeight: FontWeight.w800, letterSpacing: 2)),
        ),
        const SizedBox(height: 8),
        Wrap(spacing: 8, runSpacing: 6, alignment: WrapAlignment.center, children: [
          _OnColorPill(serving ? 'NOW SERVING' : _statusText(t.status), fg),
          if (t.isPriority) _OnColorPill('★ PRIORITY', fg),
          if (t.transferredFrom.isNotEmpty) _OnColorPill('Transferred from ${t.transferredFrom}', fg),
        ]),
      ]),
    );
  }

  static String _statusText(String s) {
    switch (s) {
      case TokenStatusValue.waiting:
        return 'WAITING';
      case TokenStatusValue.completed:
        return 'COMPLETED';
      case TokenStatusValue.noShow:
        return 'CANCELLED (NO-SHOW)';
      default:
        return 'CANCELLED';
    }
  }
}

class _OnColorPill extends StatelessWidget {
  const _OnColorPill(this.text, this.color);
  final String text;
  final Color color;

  @override
  Widget build(BuildContext context) => Container(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
        decoration: BoxDecoration(
          border: Border.all(color: color.withAlpha(160)),
          borderRadius: BorderRadius.circular(20),
        ),
        child: Text(text, style: TextStyle(color: color, fontWeight: FontWeight.w600, fontSize: 12)),
      );
}

class _Metric extends StatelessWidget {
  const _Metric({required this.label, required this.value, required this.icon});
  final String label;
  final String value;
  final IconData icon;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      elevation: 0,
      margin: EdgeInsets.zero,
      color: theme.colorScheme.surfaceContainerHighest,
      child: Padding(
        padding: const EdgeInsets.all(14),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Row(children: [
            Icon(icon, size: 16, color: theme.colorScheme.onSurfaceVariant),
            const SizedBox(width: 6),
            Flexible(child: Text(label, style: theme.textTheme.labelMedium, overflow: TextOverflow.ellipsis)),
          ]),
          const SizedBox(height: 6),
          Text(value, style: theme.textTheme.headlineSmall?.copyWith(fontWeight: FontWeight.bold)),
        ]),
      ),
    );
  }
}

class _DetailsCard extends StatelessWidget {
  const _DetailsCard({required this.token});
  final QueueToken token;

  @override
  Widget build(BuildContext context) {
    final t = token;
    Widget row(String k, String v) => Padding(
          padding: const EdgeInsets.symmetric(vertical: 4),
          child: Row(children: [
            SizedBox(width: 120, child: Text(k, style: Theme.of(context).textTheme.bodySmall)),
            Expanded(child: Text(v)),
          ]),
        );
    return Card(
      elevation: 0,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(16),
        side: BorderSide(color: Theme.of(context).colorScheme.outlineVariant),
      ),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(children: [
          row('Name', t.visitorName),
          if (t.visitorPhone.isNotEmpty) row('Phone', t.visitorPhone),
          if (t.isPriority) row('Priority', t.priorityReason.isEmpty ? 'Yes' : t.priorityReason),
          row('Issued at', formatClock(t.createdAt)),
          if (t.calledAt != null) row('Called at', formatClock(t.calledAt)),
          if (t.closedAt != null) row('Closed at', formatClock(t.closedAt)),
        ]),
      ),
    );
  }
}
