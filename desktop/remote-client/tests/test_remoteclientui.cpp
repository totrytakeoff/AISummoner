#include "appsettings.h"
#include "daemonclient.h"
#include "daemonlauncher.h"
#include "eventmodel.h"
#include "mainwindow.h"
#include "models.h"
#include "strictjson.h"
#include "theme.h"

#include <QApplication>
#include <QClipboard>
#include <QComboBox>
#include <QDir>
#include <QFile>
#include <QJsonArray>
#include <QJsonDocument>
#include <QLabel>
#include <QLocalServer>
#include <QLocalSocket>
#include <QPushButton>
#include <QSignalSpy>
#include <QStackedWidget>
#include <QTemporaryDir>
#include <QTest>

using namespace aisummoner;

namespace {

QJsonObject statusResult(const QString &phase = QStringLiteral("online"))
{
    return {{QStringLiteral("device_id"), QStringLiteral("dev_test")},
            {QStringLiteral("device_name"), QStringLiteral("workstation")},
            {QStringLiteral("client_version"), QStringLiteral("0.1.0")},
            {QStringLiteral("server_origin"), QStringLiteral("https://control.example")},
            {QStringLiteral("phase"), phase},
            {QStringLiteral("active_sessions"), 1},
            {QStringLiteral("updated_at"), QStringLiteral("2026-08-23T03:00:00Z")}};
}

QByteArray responseFrame(const QString &id, const QJsonObject &result)
{
    return QJsonDocument(QJsonObject{{QStringLiteral("version"), 1},
                                     {QStringLiteral("id"), id},
                                     {QStringLiteral("ok"), true},
                                     {QStringLiteral("result"), result}})
               .toJson(QJsonDocument::Compact)
        + '\n';
}

class FakeDaemon final : public QObject {
public:
    explicit FakeDaemon(const QString &path, QObject *parent = nullptr) : QObject(parent), path_(path)
    {
        connect(&server_, &QLocalServer::newConnection, this, [this]() {
            while (auto *socket = server_.nextPendingConnection()) {
                ++active_;
                maxActive_ = qMax(maxActive_, active_);
                auto buffer = std::make_shared<QByteArray>();
                connect(socket, &QLocalSocket::disconnected, socket, [this, socket]() {
                    --active_;
                    socket->deleteLater();
                });
                connect(socket, &QLocalSocket::readyRead, socket, [this, socket, buffer]() {
                    buffer->append(socket->readAll());
                    const auto newline = buffer->indexOf('\n');
                    if (newline < 0) return;
                    QJsonParseError error;
                    const auto document = QJsonDocument::fromJson(buffer->left(newline), &error);
                    if (error.error != QJsonParseError::NoError || !document.isObject()) return;
                    const QJsonObject request = document.object();
                    requests.push_back(request);
                    const QString method = request.value(QStringLiteral("method")).toString();
                    const QString id = request.value(QStringLiteral("id")).toString();
                    QByteArray response;
                    if (invalidDuplicate_) {
                        response = QByteArray("{\"version\":1,\"id\":\"") + id.toUtf8()
                            + "\",\"ok\":true,\"result\":{},\"result\":{}}\n";
                    } else if (oversized_) {
                        response = QByteArray(64 * 1024 + 1, 'x') + '\n';
                    } else if (method == QStringLiteral("status.get")) {
                        response = responseFrame(id, statusResult());
                    } else if (method == QStringLiteral("events.list")) {
                        const quint64 after = static_cast<quint64>(request.value(QStringLiteral("params"))
                            .toObject().value(QStringLiteral("after_sequence")).toDouble());
                        QJsonArray events;
                        quint64 next = after;
                        if (after == 0) {
                            events.append(QJsonObject{{QStringLiteral("sequence"), 1},
                                {QStringLiteral("at"), QStringLiteral("2026-08-23T03:00:01Z")},
                                {QStringLiteral("kind"), QStringLiteral("tunnel.online")},
                                {QStringLiteral("level"), QStringLiteral("info")},
                                {QStringLiteral("summary"), QStringLiteral("Connected to control service")}});
                            next = 1;
                        }
                        response = responseFrame(id, QJsonObject{
                            {QStringLiteral("events"), events},
                            {QStringLiteral("next_sequence"), static_cast<double>(next)}});
                    } else if (method == QStringLiteral("pairing.refresh")) {
                        response = responseFrame(id, {{QStringLiteral("closes_active_sessions"), true}});
                    } else {
                        response = responseFrame(id, {});
                    }
                    if (noReply_) return;
                    QPointer<QLocalSocket> guarded(socket);
                    QTimer::singleShot(delayMs_, socket, [guarded, response]() {
                        if (guarded) guarded->write(response);
                    });
                });
            }
        });
    }

    bool start()
    {
        QLocalServer::removeServer(path_);
        return server_.listen(path_);
    }
    void stop()
    {
        server_.close();
        QLocalServer::removeServer(path_);
    }

    QVector<QJsonObject> requests;
    int delayMs_ = 0;
    bool noReply_ = false;
    bool invalidDuplicate_ = false;
    bool oversized_ = false;
    int maxActive_ = 0;

private:
    QString path_;
    QLocalServer server_;
    int active_ = 0;
};

RemoteEvent makeEvent(quint64 sequence, EventCategory category)
{
    return {sequence, QDateTime::currentDateTimeUtc(),
            category == EventCategory::Control ? QStringLiteral("control_session.started")
                                               : QStringLiteral("tunnel.online"),
            EventLevel::Info, category,
            category == EventCategory::Control ? QStringLiteral("一个控制会话已开始")
                                               : QStringLiteral("已连接到控制服务")};
}

} // namespace

class RemoteClientUiTest final : public QObject {
    Q_OBJECT

private slots:
    void strictJsonRejectsAmbiguousObjects();
    void envelopeAndTypedResultsAreStrict();
    void eventModelIsBoundedAndFiltered();
    void daemonClientPollsAndRunsActionsWithoutOverlap();
    void daemonClientResetsEventStreamAfterDaemonReconnect();
    void daemonClientRejectsInvalidAndTimesOut();
    void launcherUsesOnlySafeSiblingAndExactArguments();
    void settingsPersistOnlyAllowlistedValues();
    void widgetsExposeAllStatesAndPairingSafety();
    void mainWindowHasThreePagesAndCloseDoesNotPause();
};

void RemoteClientUiTest::strictJsonRejectsAmbiguousObjects()
{
    QString error;
    QVERIFY(rejectDuplicateJsonKeys(R"({"a":1,"nested":{"b":2}})", &error));
    QVERIFY(!rejectDuplicateJsonKeys(R"({"a":1,"a":2})", &error));
    QCOMPARE(error, QStringLiteral("duplicate JSON object key"));
    QVERIFY(!rejectDuplicateJsonKeys(R"({"a":1,"\u0061":2})", &error));
    QVERIFY(!rejectDuplicateJsonKeys(R"({"outer":{"x":1,"x":2}})", &error));
    QVERIFY(!rejectDuplicateJsonKeys(R"({"a":[{"b":1,"b":2}]})", &error));
}

void RemoteClientUiTest::envelopeAndTypedResultsAreStrict()
{
    const QString id = QStringLiteral("req_test_1");
    ProtocolEnvelope envelope;
    QString error;
    QByteArray frame = responseFrame(id, statusResult()).trimmed();
    QVERIFY(parseProtocolEnvelope(frame, id, &envelope, &error));
    QVERIFY(envelope.ok);
    RemoteStatus status;
    QVERIFY(parseRemoteStatus(envelope.result, &status, &error));
    QCOMPARE(status.phase, DaemonPhase::Online);
    QCOMPARE(status.activeSessions, 1);

    QJsonObject withPairing = statusResult();
    withPairing.insert(QStringLiteral("pairing"), QJsonObject{
        {QStringLiteral("code"), QStringLiteral("ABCD-EFGH")},
        {QStringLiteral("expires_at"), QStringLiteral("2026-08-23T03:10:00Z")},
        {QStringLiteral("expired"), false}});
    QVERIFY(parseRemoteStatus(withPairing, &status, &error));
    QCOMPARE(status.pairing->code, QStringLiteral("ABCD-EFGH"));

    QJsonObject unknown = withPairing;
    unknown.insert(QStringLiteral("private_key"), QStringLiteral("sentinel"));
    QVERIFY(!parseRemoteStatus(unknown, &status, &error));
    QVERIFY(!error.contains(QStringLiteral("sentinel")));

    QByteArray duplicate = QByteArray("{\"version\":1,\"id\":\"") + id.toUtf8()
        + "\",\"ok\":true,\"result\":{},\"result\":{}}";
    QVERIFY(!parseProtocolEnvelope(duplicate, id, &envelope, &error));
    QVERIFY(!parseProtocolEnvelope(frame, QStringLiteral("different_id"), &envelope, &error));
    QJsonObject invalidVersion = QJsonDocument::fromJson(frame).object();
    invalidVersion.insert(QStringLiteral("version"), 2);
    QVERIFY(!parseProtocolEnvelope(QJsonDocument(invalidVersion).toJson(QJsonDocument::Compact),
                                   id, &envelope, &error));
    QJsonObject unknownEnvelope = QJsonDocument::fromJson(frame).object();
    unknownEnvelope.insert(QStringLiteral("extra"), true);
    QVERIFY(!parseProtocolEnvelope(QJsonDocument(unknownEnvelope).toJson(QJsonDocument::Compact),
                                   id, &envelope, &error));

    QJsonObject invalidPhase = statusResult(QStringLiteral("mystery"));
    QVERIFY(!parseRemoteStatus(invalidPhase, &status, &error));
    EventsPageResult eventsPage;
    QJsonObject invalidEvents{{QStringLiteral("events"), QJsonArray{QJsonObject{
        {QStringLiteral("sequence"), 1},
        {QStringLiteral("at"), QStringLiteral("2026-08-23T03:00:00Z")},
        {QStringLiteral("kind"), QStringLiteral("tunnel.online")},
        {QStringLiteral("level"), QStringLiteral("debug")},
        {QStringLiteral("summary"), QStringLiteral("not displayed")}}}},
        {QStringLiteral("next_sequence"), 1}};
    QVERIFY(!parseEventsPage(invalidEvents, &eventsPage, &error));

    const QByteArray failure = QJsonDocument(QJsonObject{
        {QStringLiteral("version"), 1}, {QStringLiteral("id"), id},
        {QStringLiteral("ok"), false},
        {QStringLiteral("error"), QJsonObject{{QStringLiteral("code"), QStringLiteral("TIMEOUT")},
                                               {QStringLiteral("message"), QStringLiteral("fixed")}}}})
        .toJson(QJsonDocument::Compact);
    QVERIFY(parseProtocolEnvelope(failure, id, &envelope, &error));
    QCOMPARE(envelope.errorCode, QStringLiteral("TIMEOUT"));
    QCOMPARE(fixedErrorText(envelope.errorCode), QStringLiteral("操作超时，请稍后重试"));
}

void RemoteClientUiTest::eventModelIsBoundedAndFiltered()
{
    EventListModel model;
    QVector<RemoteEvent> events;
    for (quint64 sequence = 1; sequence <= 240; ++sequence) {
        events.push_back(makeEvent(sequence, sequence % 2 ? EventCategory::Connection : EventCategory::Control));
    }
    model.append(events);
    QCOMPARE(model.allEvents().size(), 200);
    QCOMPARE(model.rowCount(), 200);
    QCOMPARE(model.allEvents().constFirst().sequence, quint64(41));
    model.setFilter(EventListModel::Filter::Control);
    QCOMPARE(model.rowCount(), 100);
    model.setFilter(EventListModel::Filter::Connection);
    QCOMPARE(model.rowCount(), 100);
}

void RemoteClientUiTest::daemonClientPollsAndRunsActionsWithoutOverlap()
{
    QTemporaryDir temporary;
    QVERIFY(temporary.isValid());
    const QString socketPath = temporary.filePath(QStringLiteral("daemon.sock"));
    FakeDaemon daemon(socketPath);
    daemon.delayMs_ = 180;
    QVERIFY(daemon.start());
    DaemonClient client(socketPath, nullptr, 1000, 100);
    QSignalSpy statusSpy(&client, &DaemonClient::statusChanged);
    QSignalSpy eventSpy(&client, &DaemonClient::eventsReceived);
    QSignalSpy actionSpy(&client, &DaemonClient::actionFinished);
    client.start();
    QTRY_VERIFY_WITH_TIMEOUT(client.isAvailable(), 1500);
    QTRY_VERIFY_WITH_TIMEOUT(statusSpy.count() >= 1, 1500);
    QTRY_VERIFY_WITH_TIMEOUT(eventSpy.count() >= 1, 1500);
    QTest::qWait(380);
    int statusRequests = 0;
    for (const auto &request : daemon.requests) {
        if (request.value(QStringLiteral("method")) == QStringLiteral("status.get")) ++statusRequests;
    }
    QVERIFY(statusRequests >= 2);
    QVERIFY(statusRequests <= 5);

    client.pauseDaemon();
    client.resumeDaemon(); // guarded while pause is pending
    QTRY_COMPARE_WITH_TIMEOUT(actionSpy.count(), 1, 1500);
    QCOMPARE(actionSpy.at(0).at(0).toString(), QStringLiteral("daemon.pause"));
    client.resumeDaemon();
    QTRY_COMPARE_WITH_TIMEOUT(actionSpy.count(), 2, 1500);
    client.refreshPairing();
    QTRY_COMPARE_WITH_TIMEOUT(actionSpy.count(), 3, 1500);
    QCOMPARE(actionSpy.at(2).at(0).toString(), QStringLiteral("pairing.refresh"));
    QVERIFY(actionSpy.at(2).at(1).toBool());
    client.stop();
}

void RemoteClientUiTest::daemonClientResetsEventStreamAfterDaemonReconnect()
{
    QTemporaryDir temporary;
    QVERIFY(temporary.isValid());
    const QString socketPath = temporary.filePath(QStringLiteral("daemon.sock"));
    FakeDaemon daemon(socketPath);
    QVERIFY(daemon.start());
    DaemonClient client(socketPath, nullptr, 300, 100);
    QSignalSpy eventSpy(&client, &DaemonClient::eventsReceived);
    QSignalSpy resetSpy(&client, &DaemonClient::eventStreamReset);
    client.start();
    QTRY_VERIFY_WITH_TIMEOUT(client.isAvailable(), 1000);
    QTRY_COMPARE_WITH_TIMEOUT(client.eventCursor(), quint64(1), 1000);
    QTRY_VERIFY_WITH_TIMEOUT(eventSpy.count() >= 1, 1000);

    daemon.stop();
    QTRY_VERIFY_WITH_TIMEOUT(!client.isAvailable(), 1000);
    QVERIFY(daemon.start());
    QTRY_VERIFY_WITH_TIMEOUT(client.isAvailable(), 1000);
    QTRY_COMPARE_WITH_TIMEOUT(resetSpy.count(), 1, 1000);
    QTRY_VERIFY_WITH_TIMEOUT(eventSpy.count() >= 2, 1000);
    QCOMPARE(client.eventCursor(), quint64(1));
    client.stop();
}

void RemoteClientUiTest::daemonClientRejectsInvalidAndTimesOut()
{
    QTemporaryDir temporary;
    QVERIFY(temporary.isValid());
    const QString socketPath = temporary.filePath(QStringLiteral("daemon.sock"));
    FakeDaemon daemon(socketPath);
    daemon.invalidDuplicate_ = true;
    QVERIFY(daemon.start());
    DaemonClient invalidClient(socketPath, nullptr, 300, 100);
    invalidClient.start();
    QTest::qWait(250);
    QVERIFY(!invalidClient.isAvailable());
    invalidClient.stop();
    daemon.stop();

    FakeDaemon oversized(socketPath);
    oversized.oversized_ = true;
    QVERIFY(oversized.start());
    DaemonClient oversizedClient(socketPath, nullptr, 300, 100);
    oversizedClient.start();
    QTest::qWait(250);
    QVERIFY(!oversizedClient.isAvailable());
    oversizedClient.stop();
    oversized.stop();

    FakeDaemon silent(socketPath);
    silent.noReply_ = true;
    QVERIFY(silent.start());
    DaemonClient timeoutClient(socketPath, nullptr, 100, 1000);
    QSignalSpy actionSpy(&timeoutClient, &DaemonClient::actionFinished);
    timeoutClient.pauseDaemon();
    QTRY_COMPARE_WITH_TIMEOUT(actionSpy.count(), 1, 500);
    QVERIFY(!actionSpy.at(0).at(1).toBool());
    QCOMPARE(actionSpy.at(0).at(2).toString(), QStringLiteral("本机后台服务响应超时"));
}

void RemoteClientUiTest::launcherUsesOnlySafeSiblingAndExactArguments()
{
    QTemporaryDir temporary;
    QVERIFY(temporary.isValid());
    const QString daemon = temporary.filePath(QStringLiteral("aisummoner-client"));
    QFile executable(daemon);
    QVERIFY(executable.open(QIODevice::WriteOnly));
    executable.write("test");
    executable.close();
    QVERIFY(executable.setPermissions(QFileDevice::ReadOwner | QFileDevice::WriteOwner
                                      | QFileDevice::ExeOwner | QFileDevice::ReadGroup
                                      | QFileDevice::ExeGroup | QFileDevice::ReadOther
                                      | QFileDevice::ExeOther));
    const QString data = temporary.filePath(QStringLiteral("data"));
    QString error;
    const auto spec = DaemonLauncher::buildLaunchSpec(temporary.path(), data,
        QStringLiteral("https://control.example:10001"), QStringLiteral("test-device"), &error);
    QVERIFY2(spec.has_value(), qPrintable(error));
    QCOMPARE(spec->program, QFileInfo(daemon).canonicalFilePath());
    QCOMPARE(spec->workingDirectory, QDir::homePath());
    QVERIFY(spec->workingDirectory != QFileInfo(daemon).canonicalPath());
    QCOMPARE(spec->arguments, QStringList({QStringLiteral("daemon"), QStringLiteral("--server"),
        QStringLiteral("https://control.example:10001"), QStringLiteral("--data-dir"), data,
        QStringLiteral("--name"), QStringLiteral("test-device")}));
    QVERIFY(!spec->arguments.contains(QStringLiteral("--dev")));
    QVERIFY(!spec->arguments.contains(QStringLiteral("--allow-root-dev")));
    QVERIFY(!DaemonLauncher::buildLaunchSpec(temporary.path(), data,
        QStringLiteral("http://remote.example"), QStringLiteral("name"), &error));

    QTemporaryDir outside;
    QVERIFY(outside.isValid());
    const QString outsideBinary = outside.filePath(QStringLiteral("daemon"));
    QFile other(outsideBinary);
    QVERIFY(other.open(QIODevice::WriteOnly));
    other.close();
    other.setPermissions(QFileDevice::ReadOwner | QFileDevice::WriteOwner | QFileDevice::ExeOwner);
    QVERIFY(QFile::remove(daemon));
    QVERIFY(QFile::link(outsideBinary, daemon));
    QVERIFY(!DaemonLauncher::buildLaunchSpec(temporary.path(), data,
        QStringLiteral("https://control.example"), QStringLiteral("name"), &error));
    QVERIFY(QFile::remove(daemon));
    QVERIFY(executable.open(QIODevice::WriteOnly));
    executable.close();
    executable.setPermissions(QFileDevice::ReadOwner | QFileDevice::WriteOwner | QFileDevice::ExeOwner);

    int starts = 0;
    DaemonLauncher launcher(temporary.path(), data, nullptr,
        [&starts](const QString &, const QStringList &, const QString &, qint64 *pid) {
            ++starts;
            *pid = 4321;
            return true;
        });
    launcher.startDaemon(QStringLiteral("https://control.example"), QStringLiteral("name"));
    launcher.startDaemon(QStringLiteral("https://control.example"), QStringLiteral("name"));
    QCOMPARE(starts, 1);
}

void RemoteClientUiTest::settingsPersistOnlyAllowlistedValues()
{
    QCOMPARE(AppSettings::defaultDataDirectory(),
             QDir(QDir::homePath()).filePath(QStringLiteral(".local/share/aisummoner")));
    QTemporaryDir temporary;
    QVERIFY(temporary.isValid());
    const QString file = temporary.filePath(QStringLiteral("settings.ini"));
    AppSettings settings(file);
    UserPreferences preferences{QStringLiteral("https://control.example"),
                                QStringLiteral("desktop"), ThemePreference::Dark};
    settings.save(preferences);
    QCOMPARE(settings.load().serverOrigin, preferences.serverOrigin);
    QCOMPARE(settings.load().deviceName, preferences.deviceName);
    QCOMPARE(settings.load().theme, ThemePreference::Dark);
    QFile contents(file);
    QVERIFY(contents.open(QIODevice::ReadOnly));
    const QByteArray persisted = contents.readAll();
    QVERIFY(!persisted.contains("PAIRING-SENTINEL"));
    QVERIFY(!persisted.contains("private_key"));
    QSettings raw(file, QSettings::IniFormat);
    QStringList keys = raw.allKeys();
    keys.sort();
    QCOMPARE(keys, QStringList({QStringLiteral("appearance/theme"),
                                QStringLiteral("connection/serverOrigin"),
                                QStringLiteral("device/displayName")}));
}

void RemoteClientUiTest::widgetsExposeAllStatesAndPairingSafety()
{
    StatusPage page;
    page.resize(780, 560);
    page.show();
    const QVector<DaemonPhase> phases = {DaemonPhase::Starting, DaemonPhase::Connecting,
        DaemonPhase::Online, DaemonPhase::Retrying, DaemonPhase::Paused,
        DaemonPhase::Stopped, DaemonPhase::Error};
    for (const DaemonPhase phase : phases) {
        RemoteStatus status;
        status.deviceId = QStringLiteral("dev_test");
        status.deviceName = QStringLiteral("workstation");
        status.serverOrigin = QStringLiteral("https://control.example");
        status.phase = phase;
        status.updatedAt = QDateTime::currentDateTimeUtc();
        page.setStatus(status);
        auto *pill = page.findChild<QLabel *>(QStringLiteral("StatusPill"));
        QVERIFY(pill);
        QVERIFY(pill->text().contains(phase == DaemonPhase::Online
            ? QStringLiteral("在线") : phaseLabel(phase)));
    }
    RemoteStatus paired;
    paired.deviceId = QStringLiteral("dev_test");
    paired.deviceName = QStringLiteral("workstation");
    paired.serverOrigin = QStringLiteral("https://control.example");
    paired.phase = DaemonPhase::Online;
    paired.updatedAt = QDateTime::currentDateTimeUtc();
    paired.pairing = PairingOffer{QStringLiteral("PAIR-CODE"),
        QDateTime::currentDateTimeUtc().addSecs(30), false};
    page.setStatus(paired);
    auto *pairing = page.findChild<QLabel *>(QStringLiteral("PairingCode"));
    QVERIFY(pairing && pairing->text() == QStringLiteral("PAIR-CODE"));
    auto *copy = page.findChild<QPushButton *>(QStringLiteral("CopyPairingButton"));
    auto *refresh = page.findChild<QPushButton *>(QStringLiteral("RefreshPairingButton"));
    QTest::mouseClick(copy, Qt::LeftButton);
    QCOMPARE(QGuiApplication::clipboard()->text(), QStringLiteral("PAIR-CODE"));

    // A pending action may finish while Status is not the current page.  The
    // refresh control must recover from that state when the page is shown
    // again; QWidget::isVisible() would incorrectly keep it disabled here.
    page.hide();
    page.setActionPending(true);
    page.setActionPending(false);
    QVERIFY(refresh->isEnabled());
    page.show();

    paired.pairing = PairingOffer{QStringLiteral("SHOULD-BE-CLEARED"),
        QDateTime::currentDateTimeUtc().addSecs(-1), false};
    page.setStatus(paired);
    QCOMPARE(pairing->text(), QStringLiteral("配对码已过期"));
    QVERIFY(!copy->isEnabled());
    QVERIFY(page.minimumSizeHint().width() <= 780);
}

void RemoteClientUiTest::mainWindowHasThreePagesAndCloseDoesNotPause()
{
    QTemporaryDir temporary;
    QVERIFY(temporary.isValid());
    const QString daemonPath = temporary.filePath(QStringLiteral("aisummoner-client"));
    QFile executable(daemonPath);
    QVERIFY(executable.open(QIODevice::WriteOnly));
    executable.close();
    executable.setPermissions(QFileDevice::ReadOwner | QFileDevice::WriteOwner | QFileDevice::ExeOwner);
    AppSettings settings(temporary.filePath(QStringLiteral("settings.ini")));
    settings.save({QStringLiteral("https://control.example"), QStringLiteral("device"),
                   ThemePreference::System});
    DaemonClient client(temporary.filePath(QStringLiteral("missing.sock")), nullptr, 100, 1000);
    DaemonLauncher launcher(temporary.path(), temporary.filePath(QStringLiteral("data")), nullptr,
        [](const QString &, const QStringList &, const QString &, qint64 *pid) { *pid = 1; return true; });
    QSignalSpy requestSpy(&client, &DaemonClient::requestSent);
    MainWindow window(&settings, &client, &launcher);
    window.show();
    QCOMPARE(window.minimumSize(), QSize(780, 560));
    auto *stack = window.findChild<QStackedWidget *>(QStringLiteral("PageStack"));
    QVERIFY(stack);
    QCOMPARE(stack->count(), 3);
    QTest::mouseClick(window.findChild<QPushButton *>(QStringLiteral("Navigation1")), Qt::LeftButton);
    QCOMPARE(stack->currentIndex(), 1);
    window.close();
    for (const auto &arguments : requestSpy) {
        QVERIFY(arguments.at(0).toString() != QStringLiteral("daemon.pause"));
    }
}

QTEST_MAIN(RemoteClientUiTest)
#include "test_remoteclientui.moc"
