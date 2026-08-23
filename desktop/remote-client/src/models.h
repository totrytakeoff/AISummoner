#pragma once

#include <QDateTime>
#include <QJsonObject>
#include <QString>
#include <QVector>

#include <optional>

namespace aisummoner {

enum class DaemonPhase {
    Starting,
    Connecting,
    Online,
    Retrying,
    Paused,
    Stopped,
    Error,
};

struct PairingOffer {
    QString code;
    QDateTime expiresAt;
    bool expired = false;
};

struct RemoteStatus {
    QString deviceId;
    QString deviceName;
    QString clientVersion;
    QString serverOrigin;
    DaemonPhase phase = DaemonPhase::Stopped;
    std::optional<QDateTime> onlineSince;
    std::optional<QDateTime> retryAt;
    std::optional<PairingOffer> pairing;
    int activeSessions = 0;
    QString lastErrorCategory;
    QDateTime updatedAt;
};

enum class EventLevel { Info, Warning, Error };
enum class EventCategory { Connection, Control };

struct RemoteEvent {
    quint64 sequence = 0;
    QDateTime at;
    QString kind;
    EventLevel level = EventLevel::Info;
    EventCategory category = EventCategory::Connection;
    QString summary;
};

struct EventsPageResult {
    QVector<RemoteEvent> events;
    quint64 nextSequence = 0;
};

struct ProtocolEnvelope {
    bool ok = false;
    QJsonObject result;
    QString errorCode;
};

bool parseProtocolEnvelope(const QByteArray &frame, const QString &expectedId,
                           ProtocolEnvelope *output, QString *error);
bool parseRemoteStatus(const QJsonObject &object, RemoteStatus *output, QString *error);
bool parseEventsPage(const QJsonObject &object, EventsPageResult *output, QString *error);
bool parsePairingRefresh(const QJsonObject &object, QString *error);

QString phaseKey(DaemonPhase phase);
QString phaseLabel(DaemonPhase phase);
QString fixedErrorText(const QString &code);

} // namespace aisummoner

Q_DECLARE_METATYPE(aisummoner::RemoteStatus)
Q_DECLARE_METATYPE(aisummoner::RemoteEvent)
Q_DECLARE_METATYPE(QVector<aisummoner::RemoteEvent>)
