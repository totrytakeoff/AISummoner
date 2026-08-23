#include "theme.h"

#include <QApplication>
#include <QGuiApplication>
#include <QPalette>
#include <QStyleHints>

namespace aisummoner {

bool effectiveDarkTheme(ThemePreference preference)
{
    if (preference == ThemePreference::Dark) return true;
    if (preference == ThemePreference::Light) return false;
#if QT_VERSION >= QT_VERSION_CHECK(6, 5, 0)
    return QGuiApplication::styleHints()->colorScheme() == Qt::ColorScheme::Dark;
#else
    return QGuiApplication::palette().color(QPalette::Window).lightness() < 128;
#endif
}

void applyTheme(QApplication *application, ThemePreference preference)
{
    const bool dark = effectiveDarkTheme(preference);
    const QString background = dark ? QStringLiteral("#15171A") : QStringLiteral("#F5F6F8");
    const QString surface = dark ? QStringLiteral("#1F2227") : QStringLiteral("#FFFFFF");
    const QString subtle = dark ? QStringLiteral("#292D34") : QStringLiteral("#EEF1F5");
    const QString text = dark ? QStringLiteral("#F1F2F4") : QStringLiteral("#1D1F23");
    const QString secondary = dark ? QStringLiteral("#A7ADB7") : QStringLiteral("#626871");
    const QString accent = dark ? QStringLiteral("#6B9CFF") : QStringLiteral("#2477F3");
    const QString border = dark ? QStringLiteral("#343943") : QStringLiteral("#E1E5EA");
    const QString danger = dark ? QStringLiteral("#F06B6B") : QStringLiteral("#D83B3B");

    QPalette palette;
    palette.setColor(QPalette::Window, QColor(background));
    palette.setColor(QPalette::WindowText, QColor(text));
    palette.setColor(QPalette::Base, QColor(surface));
    palette.setColor(QPalette::AlternateBase, QColor(subtle));
    palette.setColor(QPalette::Text, QColor(text));
    palette.setColor(QPalette::Button, QColor(surface));
    palette.setColor(QPalette::ButtonText, QColor(text));
    palette.setColor(QPalette::Highlight, QColor(accent));
    palette.setColor(QPalette::HighlightedText, Qt::white);
    palette.setColor(QPalette::PlaceholderText, QColor(secondary));
    application->setPalette(palette);

    application->setStyleSheet(QStringLiteral(R"QSS(
        * { font-family: system-ui, "Noto Sans CJK SC", sans-serif; font-size: 14px; }
        QMainWindow, QWidget#AppRoot { background: %1; color: %4; }
        QWidget#NavigationRail { background: %2; border-right: 1px solid %7; }
        QLabel#Brand { font-size: 18px; font-weight: 700; }
        QLabel#PageTitle { font-size: 22px; font-weight: 700; }
        QLabel#SectionTitle { font-size: 16px; font-weight: 650; }
        QLabel[secondary="true"] { color: %5; font-size: 12px; }
        QFrame[card="true"] { background: %2; border: 1px solid %7; border-radius: 16px; }
        QFrame[soft="true"] { background: %3; border: none; border-radius: 12px; }
        QPushButton { min-height: 36px; padding: 0 14px; background: %2;
                      border: 1px solid %7; border-radius: 10px; }
        QPushButton:hover { border-color: %6; }
        QPushButton:focus { border: 2px solid %6; }
        QPushButton:pressed { background: %3; }
        QPushButton:disabled { color: %5; background: %3; }
        QPushButton[primary="true"] { background: %6; color: white; border-color: %6; font-weight: 600; }
        QPushButton[danger="true"] { color: %8; }
        QPushButton[nav="true"] { text-align: left; min-height: 42px; border: none; padding-left: 14px; }
        QPushButton[nav="true"]:checked { background: %3; color: %6; font-weight: 650; }
        QLineEdit, QComboBox { min-height: 38px; padding: 0 10px; background: %2;
                              border: 1px solid %7; border-radius: 10px; }
        QLineEdit:focus, QComboBox:focus { border: 2px solid %6; }
        QListView { background: %2; border: 1px solid %7; border-radius: 14px;
                    padding: 8px; outline: none; }
        QListView::item { min-height: 42px; padding: 4px 8px; border-radius: 8px; }
        QListView::item:selected { background: %3; color: %4; }
        QProgressBar { border: none; border-radius: 3px; background: %3; max-height: 6px; }
        QProgressBar::chunk { border-radius: 3px; background: %6; }
        QLabel#PairingCode { font-family: monospace; font-size: 26px; font-weight: 700; letter-spacing: 2px; }
        QLabel#SessionCount { font-size: 30px; font-weight: 700; }
        QLabel#StatusPill { padding: 6px 12px; background: %3; border-radius: 13px; font-weight: 650; }
        QLabel#errorText { color: %8; }
        QScrollArea { border: none; background: transparent; }
        QScrollArea > QWidget > QWidget { background: transparent; }
        QToolTip { background: %2; color: %4; border: 1px solid %7; }
    )QSS").arg(background, surface, subtle, text, secondary, accent, border, danger));
}

} // namespace aisummoner
