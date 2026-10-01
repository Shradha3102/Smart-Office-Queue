import 'package:flutter/material.dart';

import '../../api/models.dart';
import '../../services/app_services.dart';
import '../../widgets/common.dart';

class TransferChoice {
  TransferChoice(this.departmentId, this.note);
  final int departmentId;
  final String note;
}

/// Pick a target department for a transfer. Paused departments are shown but disabled.
Future<TransferChoice?> showTransferDialog(
  BuildContext context, {
  required QueueToken token,
  required List<Department> departments,
}) {
  final targets = departments.where((d) => d.id != token.departmentId).toList();
  int? selected;
  final note = TextEditingController();
  return showDialog<TransferChoice>(
    context: context,
    builder: (ctx) => StatefulBuilder(
      builder: (ctx, setLocal) => AlertDialog(
        title: Text('Transfer ${token.tokenNumber}'),
        content: SizedBox(
          width: 420,
          child: SingleChildScrollView(
            child: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.stretch, children: [
              Text('${token.visitorName} will get a new number in the selected department and join the end of '
                  '${token.isPriority ? 'its priority lane' : 'its queue'}.'),
              const SizedBox(height: 12),
              ...targets.map((d) => Card(
                    elevation: 0,
                    color: selected == d.id ? Theme.of(ctx).colorScheme.primaryContainer : null,
                    child: ListTile(
                      enabled: !d.isPaused,
                      leading: Icon(departmentIcon(d.code)),
                      title: Text(d.name),
                      subtitle: Text(d.isPaused ? 'Paused - cannot accept transfers' : '${d.waitingCount} waiting'),
                      trailing: selected == d.id ? const Icon(Icons.check_circle) : null,
                      onTap: d.isPaused ? null : () => setLocal(() => selected = d.id),
                    ),
                  )),
              const SizedBox(height: 8),
              TextField(
                controller: note,
                maxLength: 200,
                decoration: const InputDecoration(labelText: 'Note (optional)', hintText: 'e.g. Needs payslip correction'),
              ),
            ]),
          ),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('Cancel')),
          FilledButton(
            onPressed: selected == null ? null : () => Navigator.pop(ctx, TransferChoice(selected!, note.text.trim())),
            child: const Text('Transfer'),
          ),
        ],
      ),
    ),
  );
}

/// Ask for a priority reason. Returns null if aborted.
Future<String?> showPriorityReasonDialog(BuildContext context) {
  String? reason;
  return showDialog<String>(
    context: context,
    builder: (ctx) => StatefulBuilder(
      builder: (ctx, setLocal) => AlertDialog(
        title: const Text('Mark as priority'),
        content: SizedBox(
          width: 360,
          child: Column(mainAxisSize: MainAxisSize.min, children: [
            const Text('Priority tokens are served before regular tokens, '
                'but never interrupt the visitor currently at the counter.'),
            const SizedBox(height: 12),
            SelectField<String>(
              label: 'Reason',
              value: reason,
              items: priorityReasons.map((r) => DropdownMenuItem(value: r, child: Text(r))).toList(),
              onChanged: (v) => setLocal(() => reason = v),
            ),
          ]),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('Cancel')),
          FilledButton(
            onPressed: reason == null ? null : () => Navigator.pop(ctx, reason),
            child: const Text('Mark priority'),
          ),
        ],
      ),
    ),
  );
}

/// Confirm a staff cancellation; returns the (possibly empty) reason, or null if aborted.
Future<String?> showCancelDialog(BuildContext context, QueueToken token) {
  final ctrl = TextEditingController();
  return showDialog<String>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: Text('Cancel ${token.tokenNumber}?'),
      content: SizedBox(
        width: 360,
        child: TextField(
          controller: ctrl,
          maxLength: 200,
          decoration: const InputDecoration(labelText: 'Reason (optional)'),
        ),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('Back')),
        FilledButton(
          style: FilledButton.styleFrom(backgroundColor: Theme.of(ctx).colorScheme.error),
          onPressed: () => Navigator.pop(ctx, ctrl.text.trim()),
          child: const Text('Cancel token'),
        ),
      ],
    ),
  );
}

/// Ask for an optional pause reason; null if aborted.
Future<String?> showPauseDialog(BuildContext context, Department d) {
  final ctrl = TextEditingController();
  const presets = ['Lunch break', 'Tea break', 'Staff meeting', 'System maintenance'];
  return showDialog<String>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: Text('Pause ${d.name}?'),
      content: SizedBox(
        width: 380,
        child: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.start, children: [
          const Text('Visitors will not be able to generate new tokens for this department. '
              'People already waiting keep their place.'),
          const SizedBox(height: 12),
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: presets.map((p) => ActionChip(label: Text(p), onPressed: () => ctrl.text = p)).toList(),
          ),
          const SizedBox(height: 12),
          TextField(
            controller: ctrl,
            maxLength: 200,
            decoration: const InputDecoration(labelText: 'Reason shown to visitors (optional)'),
          ),
        ]),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('Back')),
        FilledButton(
          style: FilledButton.styleFrom(backgroundColor: Colors.orange.shade800),
          onPressed: () => Navigator.pop(ctx, ctrl.text.trim()),
          child: const Text('Pause'),
        ),
      ],
    ),
  );
}

/// Issue a token at the counter (walk-in without a phone, phone call, etc.).
Future<TokenStatus?> showIssueTokenDialog(BuildContext context, Department d) {
  return showDialog<TokenStatus>(
    context: context,
    builder: (_) => _IssueTokenDialog(department: d),
  );
}

class _IssueTokenDialog extends StatefulWidget {
  const _IssueTokenDialog({required this.department});
  final Department department;

  @override
  State<_IssueTokenDialog> createState() => _IssueTokenDialogState();
}

class _IssueTokenDialogState extends State<_IssueTokenDialog> {
  final _formKey = GlobalKey<FormState>();
  final _name = TextEditingController();
  final _phone = TextEditingController();
  bool _priority = false;
  String? _reason;
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _name.dispose();
    _phone.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (!(_formKey.currentState?.validate() ?? false)) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final res = await services.api.staffIssueToken(
        departmentId: widget.department.id,
        visitorName: _name.text.trim(),
        visitorPhone: _phone.text.trim(),
        isPriority: _priority,
        priorityReason: _priority ? (_reason ?? '') : '',
      );
      if (mounted) Navigator.pop(context, res);
    } catch (e) {
      if (mounted) {
        setState(() {
          _error = errorText(e);
          _busy = false;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text('Issue ${widget.department.name} token'),
      content: SizedBox(
        width: 400,
        child: Form(
          key: _formKey,
          child: SingleChildScrollView(
            child: Column(mainAxisSize: MainAxisSize.min, children: [
              TextFormField(
                controller: _name,
                autofocus: true,
                textCapitalization: TextCapitalization.words,
                decoration: const InputDecoration(labelText: 'Visitor name *'),
                validator: (v) => (v ?? '').trim().length < 2 ? 'Enter the visitor name' : null,
              ),
              const SizedBox(height: 12),
              TextFormField(
                controller: _phone,
                keyboardType: TextInputType.phone,
                decoration: const InputDecoration(labelText: 'Phone (optional)'),
              ),
              SwitchListTile(
                contentPadding: EdgeInsets.zero,
                value: _priority,
                onChanged: (v) => setState(() => _priority = v),
                title: const Text('Priority token'),
              ),
              if (_priority)
                SelectField<String>(
                  label: 'Priority reason *',
                  value: _reason,
                  items: priorityReasons.map((r) => DropdownMenuItem(value: r, child: Text(r))).toList(),
                  onChanged: (v) => setState(() => _reason = v),
                  validator: (v) => v == null ? 'Select a reason' : null,
                ),
              if (_error != null) ...[
                const SizedBox(height: 8),
                Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error)),
              ],
            ]),
          ),
        ),
      ),
      actions: [
        TextButton(onPressed: _busy ? null : () => Navigator.pop(context), child: const Text('Cancel')),
        FilledButton(onPressed: _busy ? null : _submit, child: Text(_busy ? 'Issuing…' : 'Issue token')),
      ],
    );
  }
}
