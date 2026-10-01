import 'package:flutter/material.dart';

import '../../services/app_services.dart';
import '../../widgets/common.dart';
import 'console_screen.dart';
import 'dashboard_screen.dart';
import 'users_screen.dart';

/// Shows the login form or the staff area depending on the session.
class StaffGate extends StatelessWidget {
  const StaffGate({super.key});

  @override
  Widget build(BuildContext context) {
    return ListenableBuilder(
      listenable: services.session,
      builder: (context, _) => services.session.isLoggedIn ? const StaffShell() : const LoginScreen(),
    );
  }
}

class LoginScreen extends StatefulWidget {
  const LoginScreen({super.key});

  @override
  State<LoginScreen> createState() => _LoginScreenState();
}

class _LoginScreenState extends State<LoginScreen> {
  final _formKey = GlobalKey<FormState>();
  final _email = TextEditingController();
  final _password = TextEditingController();
  bool _busy = false;
  bool _obscure = true;
  String? _error;

  @override
  void dispose() {
    _email.dispose();
    _password.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (!(_formKey.currentState?.validate() ?? false)) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await services.session.login(_email.text, _password.text);
    } catch (e) {
      if (mounted) setState(() => _error = errorText(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(title: const Text('Staff login')),
      body: Center(
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(24),
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 420),
            child: Form(
              key: _formKey,
              child: AutofillGroup(
                child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
                  Icon(Icons.support_agent_rounded, size: 64, color: theme.colorScheme.primary),
                  const SizedBox(height: 12),
                  Text('Sign in to manage queues',
                      textAlign: TextAlign.center, style: theme.textTheme.titleLarge),
                  const SizedBox(height: 24),
                  TextFormField(
                    controller: _email,
                    keyboardType: TextInputType.emailAddress,
                    autofillHints: const [AutofillHints.email],
                    textInputAction: TextInputAction.next,
                    decoration: const InputDecoration(labelText: 'Email', prefixIcon: Icon(Icons.alternate_email)),
                    validator: (v) => (v ?? '').trim().contains('@') ? null : 'Enter your email',
                  ),
                  const SizedBox(height: 12),
                  TextFormField(
                    controller: _password,
                    obscureText: _obscure,
                    autofillHints: const [AutofillHints.password],
                    onFieldSubmitted: (_) => _submit(),
                    decoration: InputDecoration(
                      labelText: 'Password',
                      prefixIcon: const Icon(Icons.lock_outline),
                      suffixIcon: IconButton(
                        onPressed: () => setState(() => _obscure = !_obscure),
                        icon: Icon(_obscure ? Icons.visibility_outlined : Icons.visibility_off_outlined),
                      ),
                    ),
                    validator: (v) => (v ?? '').isEmpty ? 'Enter your password' : null,
                  ),
                  if (_error != null) ...[
                    const SizedBox(height: 12),
                    Text(_error!, style: TextStyle(color: theme.colorScheme.error)),
                  ],
                  const SizedBox(height: 20),
                  FilledButton(
                    style: FilledButton.styleFrom(minimumSize: const Size.fromHeight(52)),
                    onPressed: _busy ? null : _submit,
                    child: _busy
                        ? const SizedBox(width: 22, height: 22, child: CircularProgressIndicator(strokeWidth: 2))
                        : const Text('Sign in'),
                  ),
                  const SizedBox(height: 16),
                  Text(
                    'Demo accounts: admin@office.local / Admin@123 · it@office.local, hr@office.local, '
                    'accounts@office.local, administration@office.local / Staff@123',
                    textAlign: TextAlign.center,
                    style: theme.textTheme.bodySmall?.copyWith(color: theme.colorScheme.onSurfaceVariant),
                  ),
                ]),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// Navigation for logged-in staff: Console, Dashboard and (admins) Users.
class StaffShell extends StatefulWidget {
  const StaffShell({super.key});

  @override
  State<StaffShell> createState() => _StaffShellState();
}

class _StaffShellState extends State<StaffShell> {
  int _index = 0;

  @override
  Widget build(BuildContext context) {
    final session = services.session;
    final user = session.user!;
    final pages = <Widget>[
      const ConsoleScreen(),
      const DashboardScreen(),
      if (session.isAdmin) const UsersScreen(),
    ];
    final destinations = <(IconData, IconData, String)>[
      (Icons.desktop_windows_outlined, Icons.desktop_windows, 'Console'),
      (Icons.insights_outlined, Icons.insights, 'Dashboard'),
      if (session.isAdmin) (Icons.manage_accounts_outlined, Icons.manage_accounts, 'Users'),
    ];
    final index = _index.clamp(0, pages.length - 1);
    final wide = MediaQuery.of(context).size.width >= 900;

    final body = IndexedStack(index: index, children: pages);
    return Scaffold(
      appBar: AppBar(
        title: Text(destinations[index].$3),
        actions: [
          const ConnectionDot(),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 8),
            child: Center(
              child: Text('${user.name} · ${user.isAdmin ? 'Admin' : 'Staff'}',
                  style: Theme.of(context).textTheme.labelLarge),
            ),
          ),
          IconButton(
            tooltip: 'Log out',
            icon: const Icon(Icons.logout),
            onPressed: () async {
              final ok = await confirmDialog(context, title: 'Log out?', message: 'You will need to sign in again.', confirm: 'Log out');
              if (ok) services.session.logout();
            },
          ),
        ],
      ),
      body: wide
          ? Row(children: [
              NavigationRail(
                selectedIndex: index,
                labelType: NavigationRailLabelType.all,
                onDestinationSelected: (i) => setState(() => _index = i),
                destinations: destinations
                    .map((d) => NavigationRailDestination(icon: Icon(d.$1), selectedIcon: Icon(d.$2), label: Text(d.$3)))
                    .toList(),
              ),
              const VerticalDivider(width: 1),
              Expanded(child: body),
            ])
          : body,
      bottomNavigationBar: wide
          ? null
          : NavigationBar(
              selectedIndex: index,
              onDestinationSelected: (i) => setState(() => _index = i),
              destinations: destinations
                  .map((d) => NavigationDestination(icon: Icon(d.$1), selectedIcon: Icon(d.$2), label: d.$3))
                  .toList(),
            ),
    );
  }
}
