import 'dart:async';

import 'package:flutter/material.dart';

import '../api/api_client.dart';
import '../api/models.dart';
import '../services/app_services.dart';
import '../services/realtime.dart';

// ------------------------------------------------------------------ formatting

String formatEta(int? minutes) {
  if (minutes == null) return '-';
  if (minutes <= 0) return 'Any moment';
  if (minutes < 60) return '~$minutes min';
  final h = minutes ~/ 60;
  final m = minutes % 60;
  return m == 0 ? '~$h h' : '~$h h $m min';
}

String formatMinutes(double minutes) {
  if (minutes <= 0) return '0 min';
  if (minutes < 1) return '<1 min';
  if (minutes < 60) return '${minutes.toStringAsFixed(minutes < 10 ? 1 : 0)} min';
  final h = minutes ~/ 60;
  final m = (minutes % 60).round();
  return '${h}h ${m}m';
}

String formatClock(DateTime? t) {
  if (t == null) return '-';
  final h = t.hour.toString().padLeft(2, '0');
  final m = t.minute.toString().padLeft(2, '0');
  return '$h:$m';
}

String formatElapsed(DateTime? since) {
  if (since == null) return '';
  final d = DateTime.now().difference(since);
  if (d.inMinutes < 1) return 'just now';
  if (d.inMinutes < 60) return '${d.inMinutes} min';
  return '${d.inHours}h ${d.inMinutes % 60}m';
}

IconData departmentIcon(String code) {
  switch (code.toUpperCase()) {
    case 'IT':
      return Icons.computer_rounded;
    case 'HR':
      return Icons.groups_rounded;
    case 'ACC':
      return Icons.account_balance_wallet_rounded;
    case 'ADM':
      return Icons.admin_panel_settings_rounded;
    default:
      return Icons.support_agent_rounded;
  }
}

String errorText(Object e) => e is ApiException ? e.message : 'Something went wrong: $e';

void showSnack(BuildContext context, String message, {bool error = false}) {
  final scheme = Theme.of(context).colorScheme;
  ScaffoldMessenger.of(context)
    ..hideCurrentSnackBar()
    ..showSnackBar(SnackBar(
      content: Text(message),
      behavior: SnackBarBehavior.floating,
      backgroundColor: error ? scheme.error : null,
    ));
}

Future<bool> confirmDialog(BuildContext context,
    {required String title, required String message, String confirm = 'Confirm', bool danger = false}) async {
  final res = await showDialog<bool>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: Text(title),
      content: Text(message),
      actions: [
        TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('Back')),
        FilledButton(
          style: danger ? FilledButton.styleFrom(backgroundColor: Theme.of(ctx).colorScheme.error) : null,
          onPressed: () => Navigator.pop(ctx, true),
          child: Text(confirm),
        ),
      ],
    ),
  );
  return res ?? false;
}

// -------------------------------------------------------------- live refresh

/// Refetches when the server pushes a relevant change, on reconnect, and on a
/// slow timer (so ETAs keep ticking even without events).
mixin LiveRefresh<T extends StatefulWidget> on State<T> {
  StreamSubscription<QueueEvent>? _liveSub;
  Timer? _debounce;
  Timer? _poll;

  /// Return true if this screen cares about [event].
  bool wantsEvent(QueueEvent event) => true;

  Future<void> onLiveRefresh();

  Duration get pollInterval => const Duration(seconds: 30);

  void startLive() {
    _liveSub = services.realtime.events.listen((event) {
      if (!mounted || !(event.isResync || wantsEvent(event))) return;
      _debounce?.cancel();
      _debounce = Timer(const Duration(milliseconds: 250), () {
        if (mounted) onLiveRefresh();
      });
    });
    _poll = Timer.periodic(pollInterval, (_) {
      if (mounted) onLiveRefresh();
    });
  }

  @override
  void dispose() {
    _liveSub?.cancel();
    _debounce?.cancel();
    _poll?.cancel();
    super.dispose();
  }
}

// ------------------------------------------------------------------- widgets

class StatusChip extends StatelessWidget {
  const StatusChip(this.status, {super.key});
  final String status;

  @override
  Widget build(BuildContext context) {
    late final Color color;
    late final String label;
    switch (status) {
      case TokenStatusValue.waiting:
        color = Colors.blue;
        label = 'Waiting';
        break;
      case TokenStatusValue.serving:
        color = Colors.green;
        label = 'Now serving';
        break;
      case TokenStatusValue.completed:
        color = Colors.teal;
        label = 'Completed';
        break;
      case TokenStatusValue.noShow:
        color = Colors.deepOrange;
        label = 'No-show (cancelled)';
        break;
      default:
        color = Colors.grey;
        label = 'Cancelled';
    }
    return Pill(label: label, color: color);
  }
}

class Pill extends StatelessWidget {
  const Pill({super.key, required this.label, required this.color, this.icon});
  final String label;
  final Color color;
  final IconData? icon;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
      decoration: BoxDecoration(
        color: color.withAlpha(30),
        borderRadius: BorderRadius.circular(20),
        border: Border.all(color: color.withAlpha(110)),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (icon != null) ...[
            Icon(icon, size: 14, color: color),
            const SizedBox(width: 4),
          ],
          Text(label, style: TextStyle(color: color, fontWeight: FontWeight.w600, fontSize: 12)),
        ],
      ),
    );
  }
}

class PriorityPill extends StatelessWidget {
  const PriorityPill({super.key, this.reason = ''});
  final String reason;

  @override
  Widget build(BuildContext context) => Tooltip(
        message: reason.isEmpty ? 'Priority token' : reason,
        child: const Pill(label: 'Priority', color: Colors.purple, icon: Icons.star_rounded),
      );
}

class ConnectionDot extends StatelessWidget {
  const ConnectionDot({super.key});

  @override
  Widget build(BuildContext context) {
    return ValueListenableBuilder<bool>(
      valueListenable: services.realtime.connected,
      builder: (context, ok, _) => Tooltip(
        message: ok ? 'Live updates connected' : 'Reconnecting… (data refreshes every 30s)',
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 12),
          child: Row(mainAxisSize: MainAxisSize.min, children: [
            Icon(Icons.circle, size: 10, color: ok ? Colors.green : Colors.orange),
            const SizedBox(width: 6),
            Text(ok ? 'Live' : 'Offline', style: Theme.of(context).textTheme.labelMedium),
          ]),
        ),
      ),
    );
  }
}

class ErrorRetry extends StatelessWidget {
  const ErrorRetry({super.key, required this.message, required this.onRetry});
  final String message;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(mainAxisSize: MainAxisSize.min, children: [
          Icon(Icons.cloud_off_rounded, size: 48, color: Theme.of(context).colorScheme.error),
          const SizedBox(height: 12),
          Text(message, textAlign: TextAlign.center),
          const SizedBox(height: 12),
          FilledButton.tonalIcon(onPressed: onRetry, icon: const Icon(Icons.refresh), label: const Text('Retry')),
        ]),
      ),
    );
  }
}

class Banner2 extends StatelessWidget {
  const Banner2({super.key, required this.icon, required this.color, required this.text});
  final IconData icon;
  final Color color;
  final String text;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: color.withAlpha(28),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: color.withAlpha(90)),
      ),
      child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
        Icon(icon, color: color),
        const SizedBox(width: 10),
        Expanded(child: Text(text)),
      ]),
    );
  }
}

/// Constrains content width on large screens.
class MaxWidth extends StatelessWidget {
  const MaxWidth({super.key, required this.child, this.width = 720});
  final Widget child;
  final double width;

  @override
  Widget build(BuildContext context) =>
      Center(child: ConstrainedBox(constraints: BoxConstraints(maxWidth: width), child: child));
}

const priorityReasons = <String>[
  'Senior citizen',
  'Differently-abled',
  'Pregnant',
  'Medical emergency',
  'VIP / management',
  'Other',
];

/// A dropdown that participates in [Form] validation (version-stable alternative to
/// DropdownButtonFormField, whose `value` parameter is being renamed).
class SelectField<T> extends StatelessWidget {
  const SelectField({
    super.key,
    required this.label,
    required this.value,
    required this.items,
    required this.onChanged,
    this.validator,
    this.prefixIcon,
  });

  final String label;
  final T? value;
  final List<DropdownMenuItem<T>> items;
  final ValueChanged<T?>? onChanged;
  final String? Function(T?)? validator;
  final IconData? prefixIcon;

  @override
  Widget build(BuildContext context) {
    return FormField<T>(
      initialValue: value,
      autovalidateMode: AutovalidateMode.onUserInteraction,
      validator: validator,
      builder: (state) => InputDecorator(
        decoration: InputDecoration(
          labelText: label,
          errorText: state.errorText,
          prefixIcon: prefixIcon == null ? null : Icon(prefixIcon),
          contentPadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
        ),
        isEmpty: value == null,
        child: DropdownButtonHideUnderline(
          child: DropdownButton<T>(
            value: value,
            items: items,
            isExpanded: true,
            onChanged: onChanged == null
                ? null
                : (v) {
                    state.didChange(v);
                    onChanged!(v);
                  },
          ),
        ),
      ),
    );
  }
}
