#pragma once

#include "models.h"

#include <QJsonObject>
#include <QObject>
#include <QTimer>

#include <functional>

namespace aisummoner {

class DaemonClient final : public QObject {
    Q_OBJECT

public:
    explicit DaemonClient(const QString &socketPath, QObject *parent = nullptr,
                          int requestTimeoutMs = 5000, int pollIntervalMs = 1000);

    QString socketPath() const { return socketPath_; }
    bool isAvailable() const { return available_; }
    bool actionPending() const { return actionPending_; }
    quint64 eventCursor() const { return eventCursor_; }

    void start();
    void stop();
    void pollNow();
    void pauseDaemon();
    void resumeDaemon();
    void refreshPairing();

signals:
    void availabilityChanged(bool available);
    void statusChanged(const aisummoner::RemoteStatus &status);
    void eventsReceived(const QVector<aisummoner::RemoteEvent> &events);
    void eventStreamReset();
    void actionStateChanged(bool pending);
    void actionFinished(const QString &method, bool success, const QString &message);
    void requestSent(const QString &method);

private:
    using Completion = std::function<void(const ProtocolEnvelope &, const QString &)>;

    void sendRequest(const QString &method, const QJsonObject &params, Completion completion);
    void requestStatus();
    void requestEvents();
    void requestAction(const QString &method);
    void setAvailable(bool available);

    QString socketPath_;
    QTimer pollTimer_;
    int requestTimeoutMs_;
    bool running_ = false;
    bool available_ = false;
    bool statusPending_ = false;
    bool eventsPending_ = false;
    bool actionPending_ = false;
    bool snapshotReady_ = false;
    bool resetEventsOnReconnect_ = false;
    quint64 eventCursor_ = 0;
    quint64 requestCounter_ = 0;
};

} // namespace aisummoner
