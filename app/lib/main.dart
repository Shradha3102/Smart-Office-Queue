import 'package:flutter/material.dart';

import 'screens/home_screen.dart';
import 'services/app_services.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  services = await AppServices.init();
  runApp(const SmartQueueApp());
}

class SmartQueueApp extends StatelessWidget {
  const SmartQueueApp({super.key});

  @override
  Widget build(BuildContext context) {
    ThemeData theme(Brightness b) {
      final scheme = ColorScheme.fromSeed(seedColor: const Color(0xFF3F51B5), brightness: b);
      return ThemeData(
        useMaterial3: true,
        colorScheme: scheme,
        visualDensity: VisualDensity.adaptivePlatformDensity,
        inputDecorationTheme: const InputDecorationTheme(border: OutlineInputBorder()),
      );
    }

    return MaterialApp(
      title: 'Smart Office Queue',
      debugShowCheckedModeBanner: false,
      theme: theme(Brightness.light),
      darkTheme: theme(Brightness.dark),
      home: const HomeScreen(),
    );
  }
}
