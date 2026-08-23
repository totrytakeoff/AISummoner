#include "appsettings.h"
#include "daemonclient.h"
#include "daemonlauncher.h"
#include "mainwindow.h"
#include "theme.h"

#include <QApplication>
#include <QCoreApplication>
#include <QGuiApplication>
#include <QMessageBox>
#include <QStyleHints>

#include <unistd.h>

int main(int argc, char **argv)
{
    QApplication application(argc, argv);
    QCoreApplication::setOrganizationName(QStringLiteral("AISummoner"));
    QCoreApplication::setApplicationName(QStringLiteral("RemoteClient"));
    QCoreApplication::setApplicationVersion(QStringLiteral(AISUMMONER_GUI_VERSION));
    QGuiApplication::setDesktopFileName(QStringLiteral("aisummoner-remote"));

    if (geteuid() == 0) {
        QMessageBox::critical(nullptr, QStringLiteral("AISummoner Remote"),
                              QStringLiteral("出于安全考虑，被控客户端不能以 root 身份运行。"));
        return 1;
    }

    aisummoner::AppSettings settings;
    const auto preferences = settings.load();
    aisummoner::applyTheme(&application, preferences.theme);
#if QT_VERSION >= QT_VERSION_CHECK(6, 5, 0)
    QObject::connect(QGuiApplication::styleHints(), &QStyleHints::colorSchemeChanged,
                     &application, [&application, &settings](Qt::ColorScheme) {
        const auto current = settings.load();
        if (current.theme == aisummoner::ThemePreference::System) {
            aisummoner::applyTheme(&application, current.theme);
        }
    });
#endif

    aisummoner::DaemonClient client(aisummoner::AppSettings::defaultSocketPath());
    aisummoner::DaemonLauncher launcher(QCoreApplication::applicationDirPath(),
                                        aisummoner::AppSettings::defaultDataDirectory());
    aisummoner::MainWindow window(&settings, &client, &launcher);
    window.show();
    return application.exec();
}
