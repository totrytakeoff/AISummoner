#include "daemonclient.h"

#include <QCoreApplication>
#include <QTextStream>
#include <QTimer>

using namespace aisummoner;

int main(int argc, char **argv)
{
    QCoreApplication application(argc, argv);
    if (application.arguments().size() != 2) {
        QTextStream(stderr) << "usage: aisummoner_windows_ipc_probe <LOCAL\\pipe-name>\n";
        return 64;
    }

    bool statusReceived = false;
    bool eventReceived = false;
    DaemonClient client(application.arguments().at(1), nullptr, 3000, 200);
    QObject::connect(&client, &DaemonClient::statusChanged, &application,
                     [&](const RemoteStatus &status) {
        if (status.deviceId != QStringLiteral("dev_windows_contract")
            || status.phase != DaemonPhase::Online) {
            QTextStream(stderr) << "unexpected status payload\n";
            application.exit(2);
            return;
        }
        statusReceived = true;
        if (eventReceived) application.exit(0);
    });
    QObject::connect(&client, &DaemonClient::eventsReceived, &application,
                     [&](const QVector<RemoteEvent> &events) {
        if (events.isEmpty() || events.constFirst().sequence != 1) {
            QTextStream(stderr) << "unexpected events payload\n";
            application.exit(3);
            return;
        }
        eventReceived = true;
        if (statusReceived) application.exit(0);
    });
    QTimer::singleShot(15000, &application, [&]() {
        QTextStream(stderr) << "Qt-to-Go named-pipe probe timed out\n";
        application.exit(4);
    });
    client.start();
    return application.exec();
}

