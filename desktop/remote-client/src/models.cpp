#include "models.h"

#include "strictjson.h"

#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonParseError>
#include <QMap>
#include <QSet>

#include <cmath>
#include <limits>

namespace aisummoner {
namespace {

constexpr qsizetype maxFrameBytes = qsizetype{64} * 1024;

bool fail(QString *error, const QString &message = QStringLiteral("invalid local daemon response"))
{
    if (error) {
        *error = message;
    }
    return false;
}

bool exactKeys(const QJsonObject &object, const QSet<QString> &required,
               const QSet<QString> &optional = {})
{
    QSet<QString> actual;
    for (const QString &key : object.keys()) {
        actual.insert(key);
    }
    QSet<QString> missing = required;
    if (!missing.subtract(actual).isEmpty()) {
        return false;
    }
    QSet<QString> allowed = required;
    allowed.unite(optional);
    return actual.subtract(allowed).isEmpty();
}

bool boundedString(const QJsonValue &value, QString *output, qsizetype minimum, qsizetype maximum)
{
    if (!value.isString()) {
        return false;
    }
    const QString text = value.toString();
    if (text.size() < minimum || text.size() > maximum) {
        return false;
    }
    for (const QChar character : text) {
        if (character.category() == QChar::Other_Control) {
            return false;
        }
    }
    *output = text;
    return true;
}

bool parseTimestamp(const QJsonValue &value, QDateTime *output)
{
    if (!value.isString()) {
        return false;
    }
    QDateTime parsed = QDateTime::fromString(value.toString(), Qt::ISODateWithMs);
    if (!parsed.isValid()) {
        parsed = QDateTime::fromString(value.toString(), Qt::ISODate);
    }
    if (!parsed.isValid() || parsed.timeSpec() == Qt::LocalTime) {
        return false;
    }
    *output = parsed.toUTC();
    return true;
}

bool parseUnsigned(const QJsonValue &value, quint64 *output)
{
    if (!value.isDouble()) {
        return false;
    }
    const double number = value.toDouble();
    constexpr double maxExactInteger = 9007199254740991.0;
    if (!std::isfinite(number) || number < 0 || number > maxExactInteger || std::floor(number) != number) {
        return false;
    }
    *output = static_cast<quint64>(number);
    return true;
}

std::optional<DaemonPhase> parsePhase(const QString &value)
{
    if (value == QStringLiteral("starting")) return DaemonPhase::Starting;
    if (value == QStringLiteral("connecting")) return DaemonPhase::Connecting;
    if (value == QStringLiteral("online")) return DaemonPhase::Online;
    if (value == QStringLiteral("retrying")) return DaemonPhase::Retrying;
    if (value == QStringLiteral("paused")) return DaemonPhase::Paused;
    if (value == QStringLiteral("stopped")) return DaemonPhase::Stopped;
    if (value == QStringLiteral("error")) return DaemonPhase::Error;
    return std::nullopt;
}

struct EventPresentation {
    EventCategory category;
    QString summary;
};

std::optional<EventPresentation> eventPresentation(const QString &kind)
{
    static const QMap<QString, EventPresentation> known = {
        {QStringLiteral("daemon.started"), {EventCategory::Connection, QStringLiteral("后台服务已启动")}},
        {QStringLiteral("daemon.paused"), {EventCategory::Connection, QStringLiteral("远程控制已暂停")}},
        {QStringLiteral("daemon.error"), {EventCategory::Connection, QStringLiteral("后台服务需要检查")}},
        {QStringLiteral("daemon.resumed"), {EventCategory::Connection, QStringLiteral("远程控制已恢复")}},
        {QStringLiteral("daemon.stopped"), {EventCategory::Connection, QStringLiteral("后台服务已停止")}},
        {QStringLiteral("tunnel.starting"), {EventCategory::Connection, QStringLiteral("正在准备连接")}},
        {QStringLiteral("tunnel.connecting"), {EventCategory::Connection, QStringLiteral("正在连接控制服务")}},
        {QStringLiteral("tunnel.online"), {EventCategory::Connection, QStringLiteral("已连接到控制服务")}},
        {QStringLiteral("tunnel.retrying"), {EventCategory::Connection, QStringLiteral("连接中断，正在重试")}},
        {QStringLiteral("pairing.refresh_requested"), {EventCategory::Connection, QStringLiteral("已请求新的配对码")}},
        {QStringLiteral("pairing.available"), {EventCategory::Connection, QStringLiteral("新的配对码已就绪")}},
        {QStringLiteral("pairing.expired"), {EventCategory::Connection, QStringLiteral("配对码已过期")}},
        {QStringLiteral("control_session.started"), {EventCategory::Control, QStringLiteral("一个控制会话已开始")}},
        {QStringLiteral("control_session.ended"), {EventCategory::Control, QStringLiteral("一个控制会话已结束")}},
    };
    const auto iterator = known.constFind(kind);
    if (iterator == known.cend()) {
        return std::nullopt;
    }
    return *iterator;
}

} // namespace

bool parseProtocolEnvelope(const QByteArray &frame, const QString &expectedId,
                           ProtocolEnvelope *output, QString *error)
{
    if (!output || frame.isEmpty() || frame.size() >= maxFrameBytes) {
        return fail(error);
    }
    QString duplicateError;
    if (!rejectDuplicateJsonKeys(frame, &duplicateError)) {
        return fail(error);
    }
    QJsonParseError parseError;
    const QJsonDocument document = QJsonDocument::fromJson(frame, &parseError);
    if (parseError.error != QJsonParseError::NoError || !document.isObject()) {
        return fail(error);
    }
    const QJsonObject object = document.object();
    if (!object.value(QStringLiteral("version")).isDouble()
        || object.value(QStringLiteral("version")).toInt(-1) != 1
        || !object.value(QStringLiteral("id")).isString()
        || object.value(QStringLiteral("id")).toString() != expectedId
        || !object.value(QStringLiteral("ok")).isBool()) {
        return fail(error);
    }
    ProtocolEnvelope parsed;
    parsed.ok = object.value(QStringLiteral("ok")).toBool();
    if (parsed.ok) {
        if (!exactKeys(object, {QStringLiteral("version"), QStringLiteral("id"),
                                QStringLiteral("ok"), QStringLiteral("result")})
            || !object.value(QStringLiteral("result")).isObject()) {
            return fail(error);
        }
        parsed.result = object.value(QStringLiteral("result")).toObject();
    } else {
        if (!exactKeys(object, {QStringLiteral("version"), QStringLiteral("id"),
                                QStringLiteral("ok"), QStringLiteral("error")})
            || !object.value(QStringLiteral("error")).isObject()) {
            return fail(error);
        }
        const QJsonObject remoteError = object.value(QStringLiteral("error")).toObject();
        if (!exactKeys(remoteError, {QStringLiteral("code"), QStringLiteral("message")})
            || !remoteError.value(QStringLiteral("code")).isString()
            || !remoteError.value(QStringLiteral("message")).isString()) {
            return fail(error);
        }
        static const QSet<QString> codes = {
            QStringLiteral("INVALID_REQUEST"), QStringLiteral("METHOD_NOT_FOUND"),
            QStringLiteral("TIMEOUT"), QStringLiteral("NO_PAIRING_OFFER"),
            QStringLiteral("DAEMON_UNAVAILABLE"), QStringLiteral("OPERATION_FAILED"),
            QStringLiteral("INTERNAL_ERROR"),
        };
        parsed.errorCode = remoteError.value(QStringLiteral("code")).toString();
        if (!codes.contains(parsed.errorCode)
            || remoteError.value(QStringLiteral("message")).toString().isEmpty()
            || remoteError.value(QStringLiteral("message")).toString().size() > 160) {
            return fail(error);
        }
    }
    *output = parsed;
    return true;
}

bool parseRemoteStatus(const QJsonObject &object, RemoteStatus *output, QString *error)
{
    if (!output || !exactKeys(object,
            {QStringLiteral("device_id"), QStringLiteral("device_name"),
             QStringLiteral("client_version"), QStringLiteral("server_origin"),
             QStringLiteral("phase"), QStringLiteral("active_sessions"), QStringLiteral("updated_at")},
            {QStringLiteral("online_since"), QStringLiteral("retry_at"),
             QStringLiteral("pairing"), QStringLiteral("last_error_category")})) {
        return fail(error);
    }
    RemoteStatus parsed;
    if (!boundedString(object.value(QStringLiteral("device_id")), &parsed.deviceId, 1, 128)
        || !boundedString(object.value(QStringLiteral("device_name")), &parsed.deviceName, 1, 128)
        || !boundedString(object.value(QStringLiteral("client_version")), &parsed.clientVersion, 1, 64)
        || !boundedString(object.value(QStringLiteral("server_origin")), &parsed.serverOrigin, 1, 2048)
        || !object.value(QStringLiteral("phase")).isString()
        || !parseTimestamp(object.value(QStringLiteral("updated_at")), &parsed.updatedAt)) {
        return fail(error);
    }
    const auto phase = parsePhase(object.value(QStringLiteral("phase")).toString());
    if (!phase) {
        return fail(error);
    }
    parsed.phase = *phase;
    quint64 activeSessions = 0;
    if (!parseUnsigned(object.value(QStringLiteral("active_sessions")), &activeSessions)
        || activeSessions > std::numeric_limits<int>::max()) {
        return fail(error);
    }
    parsed.activeSessions = static_cast<int>(activeSessions);
    for (const auto &field : {QStringLiteral("online_since"), QStringLiteral("retry_at")}) {
        if (!object.contains(field)) continue;
        QDateTime timestamp;
        if (!parseTimestamp(object.value(field), &timestamp)) return fail(error);
        if (field == QStringLiteral("online_since")) parsed.onlineSince = timestamp;
        else parsed.retryAt = timestamp;
    }
    if (object.contains(QStringLiteral("last_error_category"))) {
        if (!boundedString(object.value(QStringLiteral("last_error_category")),
                           &parsed.lastErrorCategory, 1, 64)) {
            return fail(error);
        }
    }
    if (object.contains(QStringLiteral("pairing"))) {
        if (!object.value(QStringLiteral("pairing")).isObject()) return fail(error);
        const QJsonObject pairing = object.value(QStringLiteral("pairing")).toObject();
        if (!exactKeys(pairing, {QStringLiteral("expires_at"), QStringLiteral("expired")},
                       {QStringLiteral("code")})
            || !pairing.value(QStringLiteral("expired")).isBool()) {
            return fail(error);
        }
        PairingOffer offer;
        offer.expired = pairing.value(QStringLiteral("expired")).toBool();
        if (!parseTimestamp(pairing.value(QStringLiteral("expires_at")), &offer.expiresAt)) {
            return fail(error);
        }
        if (pairing.contains(QStringLiteral("code"))) {
            if (!boundedString(pairing.value(QStringLiteral("code")), &offer.code, 1, 128)) {
                return fail(error);
            }
        }
        if ((offer.expired && !offer.code.isEmpty()) || (!offer.expired && offer.code.isEmpty())) {
            return fail(error);
        }
        parsed.pairing = offer;
    }
    *output = parsed;
    return true;
}

bool parseEventsPage(const QJsonObject &object, EventsPageResult *output, QString *error)
{
    if (!output || !exactKeys(object, {QStringLiteral("events"), QStringLiteral("next_sequence")})
        || !object.value(QStringLiteral("events")).isArray()) {
        return fail(error);
    }
    EventsPageResult parsed;
    if (!parseUnsigned(object.value(QStringLiteral("next_sequence")), &parsed.nextSequence)) {
        return fail(error);
    }
    const QJsonArray events = object.value(QStringLiteral("events")).toArray();
    if (events.size() > 200) return fail(error);
    quint64 previous = 0;
    for (const auto value : events) {
        if (!value.isObject()) return fail(error);
        const QJsonObject event = value.toObject();
        if (!exactKeys(event, {QStringLiteral("sequence"), QStringLiteral("at"),
                               QStringLiteral("kind"), QStringLiteral("level"), QStringLiteral("summary")})) {
            return fail(error);
        }
        RemoteEvent item;
        QString ignoredSummary;
        if (!parseUnsigned(event.value(QStringLiteral("sequence")), &item.sequence)
            || item.sequence == 0 || (previous != 0 && item.sequence <= previous)
            || !parseTimestamp(event.value(QStringLiteral("at")), &item.at)
            || !boundedString(event.value(QStringLiteral("kind")), &item.kind, 1, 80)
            || !boundedString(event.value(QStringLiteral("summary")), &ignoredSummary, 1, 256)
            || !event.value(QStringLiteral("level")).isString()) {
            return fail(error);
        }
        previous = item.sequence;
        const auto presentation = eventPresentation(item.kind);
        if (!presentation) return fail(error);
        item.category = presentation->category;
        item.summary = presentation->summary;
        const QString level = event.value(QStringLiteral("level")).toString();
        if (level == QStringLiteral("info")) item.level = EventLevel::Info;
        else if (level == QStringLiteral("warning")) item.level = EventLevel::Warning;
        else if (level == QStringLiteral("error")) item.level = EventLevel::Error;
        else return fail(error);
        parsed.events.push_back(item);
    }
    if (!parsed.events.isEmpty() && parsed.nextSequence != parsed.events.constLast().sequence) {
        return fail(error);
    }
    *output = parsed;
    return true;
}

bool parsePairingRefresh(const QJsonObject &object, QString *error)
{
    if (!exactKeys(object, {QStringLiteral("closes_active_sessions")})
        || !object.value(QStringLiteral("closes_active_sessions")).isBool()
        || !object.value(QStringLiteral("closes_active_sessions")).toBool()) {
        return fail(error);
    }
    return true;
}

QString phaseKey(DaemonPhase phase)
{
    switch (phase) {
    case DaemonPhase::Starting: return QStringLiteral("starting");
    case DaemonPhase::Connecting: return QStringLiteral("connecting");
    case DaemonPhase::Online: return QStringLiteral("online");
    case DaemonPhase::Retrying: return QStringLiteral("retrying");
    case DaemonPhase::Paused: return QStringLiteral("paused");
    case DaemonPhase::Stopped: return QStringLiteral("stopped");
    case DaemonPhase::Error: return QStringLiteral("error");
    }
    return QStringLiteral("stopped");
}

QString phaseLabel(DaemonPhase phase)
{
    switch (phase) {
    case DaemonPhase::Starting: return QStringLiteral("正在启动");
    case DaemonPhase::Connecting: return QStringLiteral("正在连接");
    case DaemonPhase::Online: return QStringLiteral("在线");
    case DaemonPhase::Retrying: return QStringLiteral("正在重连");
    case DaemonPhase::Paused: return QStringLiteral("已暂停");
    case DaemonPhase::Stopped: return QStringLiteral("已停止");
    case DaemonPhase::Error: return QStringLiteral("连接错误");
    }
    return QStringLiteral("状态未知");
}

QString fixedErrorText(const QString &code)
{
    static const QMap<QString, QString> messages = {
        {QStringLiteral("INVALID_REQUEST"), QStringLiteral("本地请求无效")},
        {QStringLiteral("METHOD_NOT_FOUND"), QStringLiteral("当前后台版本不支持此操作")},
        {QStringLiteral("TIMEOUT"), QStringLiteral("操作超时，请稍后重试")},
        {QStringLiteral("NO_PAIRING_OFFER"), QStringLiteral("当前没有可刷新的配对码")},
        {QStringLiteral("DAEMON_UNAVAILABLE"), QStringLiteral("后台服务不可用")},
        {QStringLiteral("OPERATION_FAILED"), QStringLiteral("操作失败，请稍后重试")},
        {QStringLiteral("INTERNAL_ERROR"), QStringLiteral("后台服务发生错误")},
        {QStringLiteral("LOCAL_UNAVAILABLE"), QStringLiteral("无法连接本机后台服务")},
        {QStringLiteral("LOCAL_TIMEOUT"), QStringLiteral("本机后台服务响应超时")},
        {QStringLiteral("INVALID_RESPONSE"), QStringLiteral("后台服务返回了无效响应")},
    };
    return messages.value(code, QStringLiteral("操作未完成"));
}

} // namespace aisummoner
