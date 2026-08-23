#pragma once

#include <QSettings>
#include <QString>

#include <memory>

namespace aisummoner {

enum class ThemePreference { System, Light, Dark };

struct UserPreferences {
    QString serverOrigin;
    QString deviceName;
    ThemePreference theme = ThemePreference::System;
};

class AppSettings final {
public:
    explicit AppSettings(const QString &fileName = {});

    UserPreferences load() const;
    void save(const UserPreferences &preferences);
    QString fileName() const;

    static QString defaultDataDirectory();
    static QString defaultSocketPath();

private:
    std::unique_ptr<QSettings> settings_;
};

QString themeKey(ThemePreference theme);
ThemePreference themeFromKey(const QString &key);

} // namespace aisummoner
