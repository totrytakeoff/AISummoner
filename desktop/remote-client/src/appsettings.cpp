#include "appsettings.h"

#include <QDir>

namespace aisummoner {

AppSettings::AppSettings(const QString &fileName)
{
    if (fileName.isEmpty()) {
        settings_ = std::make_unique<QSettings>(QStringLiteral("AISummoner"),
                                                QStringLiteral("RemoteClient"));
    } else {
        settings_ = std::make_unique<QSettings>(fileName, QSettings::IniFormat);
    }
}

UserPreferences AppSettings::load() const
{
    UserPreferences preferences;
    preferences.serverOrigin = settings_->value(QStringLiteral("connection/serverOrigin")).toString();
    preferences.deviceName = settings_->value(QStringLiteral("device/displayName")).toString();
    preferences.theme = themeFromKey(settings_->value(QStringLiteral("appearance/theme"),
                                                       QStringLiteral("system")).toString());
    return preferences;
}

void AppSettings::save(const UserPreferences &preferences)
{
    settings_->setValue(QStringLiteral("connection/serverOrigin"), preferences.serverOrigin);
    settings_->setValue(QStringLiteral("device/displayName"), preferences.deviceName);
    settings_->setValue(QStringLiteral("appearance/theme"), themeKey(preferences.theme));
    settings_->sync();
}

QString AppSettings::fileName() const
{
    return settings_->fileName();
}

QString AppSettings::defaultDataDirectory()
{
    // Keep the GUI and the Go CLI on the same fixed default even when a custom
    // XDG_DATA_HOME is present.  Local control commands must find the daemon's
    // socket without a hidden GUI-only path rule.
    return QDir(QDir::homePath()).filePath(QStringLiteral(".local/share/aisummoner"));
}

QString AppSettings::defaultSocketPath()
{
    return QDir(defaultDataDirectory()).filePath(QStringLiteral("client.sock"));
}

QString themeKey(ThemePreference theme)
{
    switch (theme) {
    case ThemePreference::System: return QStringLiteral("system");
    case ThemePreference::Light: return QStringLiteral("light");
    case ThemePreference::Dark: return QStringLiteral("dark");
    }
    return QStringLiteral("system");
}

ThemePreference themeFromKey(const QString &key)
{
    if (key == QStringLiteral("light")) return ThemePreference::Light;
    if (key == QStringLiteral("dark")) return ThemePreference::Dark;
    return ThemePreference::System;
}

} // namespace aisummoner
