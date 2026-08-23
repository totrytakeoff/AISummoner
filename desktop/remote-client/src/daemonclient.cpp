#include "daemonclient.h"

#include <QDir>
#include <QJsonDocument>
#include <QLocalSocket>
#include <QPointer>
#include <QRandomGenerator>

#include <memory>

namespace aisummoner {
namespace {

constexpr qsizetype maxFrameBytes = qsizetype{64} * 1024;

struct PendingCall {
    QPointer<QLocalSocket> socket;
    QByteArray response;
    bool completed = false;
};

bool emptyResult(const QJsonObject &object)
{
    return object.isEmpty();
}

} // namespace

DaemonClient::DaemonClient(const QString &socketPath, QObject *parent,
                           int requestTimeoutMs, int pollIntervalMs)
    : QObject(parent), socketPath_(QDir::cleanPath(socketPath)),
      requestTimeoutMs_(qMax(100, requestTimeoutMs))
{
    pollTimer_.setParent(this);
    pollTimer_.setInterval(qMax(100, pollIntervalMs));
    pollTimer_.setSingleShot(false);
    connect(&pollTimer_, &QTimer::timeout, this, &DaemonClient::pollNow);
}

void DaemonClient::start()
{
    if (running_) return;
    running_ = true;
    pollTimer_.start();
    pollNow();
}

void DaemonClient::stop()
{
    running_ = false;
    pollTimer_.stop();
    // Closing the GUI deliberately sends no daemon command. Outstanding local
    // sockets are QObject children and are simply closed on destruction.
}

void DaemonClient::pollNow()
{
    if (!running_) return;
    requestStatus();
    if (snapshotReady_) requestEvents();
}

void DaemonClient::pauseDaemon()
{
    requestAction(QStringLiteral("daemon.pause"));
}

void DaemonClient::resumeDaemon()
{
    requestAction(QStringLiteral("daemon.resume"));
}

void DaemonClient::refreshPairing()
{
    requestAction(QStringLiteral("pairing.refresh"));
}

void DaemonClient::sendRequest(const QString &method, const QJsonObject &params, Completion completion)
{
    const QString requestId = QStringLiteral("gui_%1_%2")
        .arg(++requestCounter_, 1, 16)
        .arg(QRandomGenerator::global()->generate(), 8, 16, QLatin1Char('0'));
    QJsonObject request;
    request.insert(QStringLiteral("version"), 1);
    request.insert(QStringLiteral("id"), requestId);
    request.insert(QStringLiteral("method"), method);
    request.insert(QStringLiteral("params"), params);
    QByteArray frame = QJsonDocument(request).toJson(QJsonDocument::Compact);
    frame.append('\n');
    if (frame.size() > maxFrameBytes || !QDir::isAbsolutePath(socketPath_) || socketPath_.size() > 100) {
        completion({}, QStringLiteral("INVALID_RESPONSE"));
        return;
    }

    emit requestSent(method);
    auto state = std::make_shared<PendingCall>();
    auto *socket = new QLocalSocket(this);
    state->socket = socket;
    auto *timer = new QTimer(socket);
    timer->setSingleShot(true);

    const auto finish = [state, completion = std::move(completion)](
                            const ProtocolEnvelope &envelope, const QString &errorCode) {
        if (state->completed) return;
        state->completed = true;
        if (state->socket) {
            state->socket->abort();
            state->socket->deleteLater();
        }
        completion(envelope, errorCode);
    };

    connect(timer, &QTimer::timeout, socket, [finish]() mutable {
        finish({}, QStringLiteral("LOCAL_TIMEOUT"));
    });
    connect(socket, &QLocalSocket::connected, socket, [socket, frame]() {
        socket->write(frame);
    });
    connect(socket, &QLocalSocket::readyRead, socket, [state, requestId, finish]() mutable {
        if (!state->socket || state->completed) return;
        state->response.append(state->socket->readAll());
        if (state->response.size() > maxFrameBytes) {
            finish({}, QStringLiteral("INVALID_RESPONSE"));
            return;
        }
        const qsizetype newline = state->response.indexOf('\n');
        if (newline < 0) return;
        if (newline == 0 || newline + 1 != state->response.size()) {
            finish({}, QStringLiteral("INVALID_RESPONSE"));
            return;
        }
        ProtocolEnvelope envelope;
        QString parseError;
        if (!parseProtocolEnvelope(state->response.left(newline), requestId, &envelope, &parseError)) {
            finish({}, QStringLiteral("INVALID_RESPONSE"));
            return;
        }
        finish(envelope, {});
    });
    connect(socket, &QLocalSocket::errorOccurred, socket,
            [state, finish](QLocalSocket::LocalSocketError) mutable {
        if (!state->completed) finish({}, QStringLiteral("LOCAL_UNAVAILABLE"));
    });
    timer->start(requestTimeoutMs_);
    socket->connectToServer(socketPath_, QIODevice::ReadWrite);
}

void DaemonClient::requestStatus()
{
    if (!running_ || statusPending_) return;
    statusPending_ = true;
    sendRequest(QStringLiteral("status.get"), {}, [this](const ProtocolEnvelope &envelope, const QString &localError) {
        statusPending_ = false;
        if (!localError.isEmpty() || !envelope.ok) {
            if (available_ || snapshotReady_) resetEventsOnReconnect_ = true;
            snapshotReady_ = false;
            setAvailable(false);
            return;
        }
        RemoteStatus status;
        QString parseError;
        if (!parseRemoteStatus(envelope.result, &status, &parseError)) {
            if (available_ || snapshotReady_) resetEventsOnReconnect_ = true;
            snapshotReady_ = false;
            setAvailable(false);
            return;
        }
        if (resetEventsOnReconnect_) {
            eventCursor_ = 0;
            resetEventsOnReconnect_ = false;
            emit eventStreamReset();
        }
        snapshotReady_ = true;
        setAvailable(true);
        emit statusChanged(status);
        requestEvents();
    });
}

void DaemonClient::requestEvents()
{
    if (!running_ || eventsPending_ || !snapshotReady_) return;
    eventsPending_ = true;
    QJsonObject params;
    params.insert(QStringLiteral("after_sequence"), static_cast<double>(eventCursor_));
    params.insert(QStringLiteral("limit"), 200);
    sendRequest(QStringLiteral("events.list"), params,
                [this](const ProtocolEnvelope &envelope, const QString &localError) {
        eventsPending_ = false;
        if (!localError.isEmpty() || !envelope.ok) return;
        EventsPageResult page;
        QString parseError;
        if (!parseEventsPage(envelope.result, &page, &parseError)) {
            return;
        }
        // A daemon can restart between successful status polls and reset its
        // in-memory event sequence.  Treat a lower cursor as a new event stream
        // and fetch its complete bounded snapshot instead of ignoring it forever.
        if (page.nextSequence < eventCursor_) {
            eventCursor_ = 0;
            emit eventStreamReset();
            requestEvents();
            return;
        }
        if (!page.events.isEmpty() && page.events.constFirst().sequence <= eventCursor_) return;
        eventCursor_ = page.nextSequence;
        if (!page.events.isEmpty()) emit eventsReceived(page.events);
    });
}

void DaemonClient::requestAction(const QString &method)
{
    if (actionPending_) return;
    actionPending_ = true;
    emit actionStateChanged(true);
    sendRequest(method, {}, [this, method](const ProtocolEnvelope &envelope, const QString &localError) {
        actionPending_ = false;
        emit actionStateChanged(false);
        if (!localError.isEmpty()) {
            emit actionFinished(method, false, fixedErrorText(localError));
            return;
        }
        if (!envelope.ok) {
            emit actionFinished(method, false, fixedErrorText(envelope.errorCode));
            return;
        }
        QString validationError;
        const bool valid = method == QStringLiteral("pairing.refresh")
            ? parsePairingRefresh(envelope.result, &validationError)
            : emptyResult(envelope.result);
        if (!valid) {
            emit actionFinished(method, false, fixedErrorText(QStringLiteral("INVALID_RESPONSE")));
            return;
        }
        emit actionFinished(method, true, QStringLiteral("操作已完成"));
        pollNow();
    });
}

void DaemonClient::setAvailable(bool available)
{
    if (available_ == available) return;
    available_ = available;
    emit availabilityChanged(available_);
}

} // namespace aisummoner
