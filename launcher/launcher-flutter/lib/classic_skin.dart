import 'dart:ui' as ui;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

// Original launcher artwork; crop only the decorative, text-free regions.
final _art = () async {
  final bytes = await rootBundle.load('assets/classic/background.png');
  final codec = await ui.instantiateImageCodec(bytes.buffer.asUint8List());
  final frame = await codec.getNextFrame();
  codec.dispose();
  return frame.image;
}();

class ClassicBackdrop extends StatelessWidget {
  const ClassicBackdrop({super.key, required this.child});
  final Widget child;
  @override
  Widget build(BuildContext context) => FutureBuilder<ui.Image>(
      future: _art,
      builder: (context, snapshot) =>
          CustomPaint(painter: _ClassicPainter(snapshot.data), child: child));
}

class ClassicProgress extends StatelessWidget {
  const ClassicProgress({super.key, this.value});
  final double? value;
  @override
  Widget build(BuildContext context) => Semantics(
      label: '更新进度',
      value: value == null ? '等待更新' : '${(value! * 100).round()}%',
      child: FutureBuilder<ui.Image>(
          future: _art,
          builder: (context, snapshot) => SizedBox(
              height: 50,
              child: CustomPaint(
                  size: const Size(double.infinity, 50),
                  painter: _ClassicPainter(snapshot.data,
                      arrow: true, progress: value ?? 0)))));
}

class _ClassicPainter extends CustomPainter {
  _ClassicPainter(this.image, {this.arrow = false, this.progress = 0});
  final ui.Image? image;
  final bool arrow;
  final double progress;
  @override
  void paint(Canvas canvas, Size size) {
    if (image == null) {
      if (!arrow) canvas.drawColor(const Color(0xff9b1d00), BlendMode.src);
      return;
    }
    final target = Offset.zero & size;
    if (!arrow) {
      canvas.drawRect(target, Paint()..shader = const LinearGradient(
        begin: Alignment.topCenter, end: Alignment.bottomCenter,
        colors: [Color(0xffb32600), Color(0xff8e1a00), Color(0xff4f0d00)],
      ).createShader(target));
      final textureHeight = size.width * 85 / 600;
      canvas.drawImageRect(image!, const Rect.fromLTWH(0, 460, 600, 85),
        Rect.fromLTWH(0, size.height - textureHeight - 70, size.width, textureHeight),
        Paint()..filterQuality = FilterQuality.high);
      return;
    }
    canvas.drawImageRect(
        image!,
        arrow
            ? const Rect.fromLTWH(0, 545, 445, 50)
            : const Rect.fromLTWH(0, 460, 600, 85),
        target,
        Paint()..filterQuality = FilterQuality.high);
    if (arrow && progress > 0) {
      canvas.save();
      canvas.scale(size.width / 445, size.height / 50);
      final shape = Path()
        ..moveTo(19, 27)
        ..lineTo(154, 34)
        ..lineTo(231, 18)
        ..lineTo(280, 30)
        ..lineTo(375, 13)
        ..lineTo(374, 7)
        ..lineTo(426, 19)
        ..lineTo(386, 35)
        ..lineTo(388, 27)
        ..lineTo(277, 39)
        ..lineTo(235, 27)
        ..lineTo(160, 41)
        ..lineTo(19, 32)
        ..close();
      canvas.clipPath(shape);
      canvas.clipRect(Rect.fromLTWH(0, 0, 445 * progress.clamp(0, 1), 50));
      canvas.drawRect(
          const Rect.fromLTWH(0, 0, 445, 50),
          Paint()
            ..shader = const LinearGradient(
                    colors: [Color(0xffffed75), Color(0xffffae00)])
                .createShader(const Rect.fromLTWH(0, 0, 445, 50)));
      canvas.restore();
    }
  }

  @override
  bool shouldRepaint(_ClassicPainter old) =>
      old.image != image || old.progress != progress || old.arrow != arrow;
}
