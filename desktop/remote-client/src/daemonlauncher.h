#pragma once

#include <QObject>
#include <QString>
#include <QStringList>
#include <QTimer>

#include <functional>
#include <optional>

namespace aisummoner {

struct LaunchSpec {
    QString program;
    QStringList arguments;
    QString workingDirectory;
};

class DaemonLauncher final : public QObject {
    Q_OBJECT

public:
    using StartFunction = std::function<bool(const QString &, const QStringList &,
                                             const QString &, qint64 *)>;

    explicit DaemonLauncher(QString applicationDirectory, QString dataDirectory,
                            QObject *parent = nullptr, StartFunction starter = {});

    bool isBusy() const { return busy_; }
    void startDaemon(const QString &serverOrigin, const QString &deviceName);
    void setDaemonAvailable(bool available);

    static std::optional<LaunchSpec> buildLaunchSpec(const QString &applicationDirectory,
                                                     const QString &dataDirectory,
                                                     const QString &serverOrigin,
                                                     const QString &deviceName,
                                                     QString *error);

signals:
    void busyChanged(bool busy);
    void launchFinished(bool success, const QString &message);

private:
    QString applicationDirectory_;
    QString dataDirectory_;
    StartFunction starter_;
    QTimer guardTimer_;
    bool busy_ = false;
};

} // namespace aisummoner
