#include "daemonlauncher.h"

#include <QCoreApplication>
#include <QDir>
#include <QFileDevice>
#include <QFileInfo>
#include <QProcess>
#include <QUrl>

namespace aisummoner {
namespace {

bool safeDeviceName(const QString &name)
{
    const QByteArray encoded = name.toUtf8();
    if (encoded.isEmpty() || encoded.size() > 128 || name != name.trimmed()) return false;
    for (const QChar character : name) {
        if (character.category() == QChar::Other_Control) return false;
    }
    return true;
}

std::optional<QString> canonicalHttpsOrigin(const QString &input)
{
    if (input.isEmpty() || input != input.trimmed() || input.size() > 2048) return std::nullopt;
    const QUrl url(input, QUrl::StrictMode);
    if (!url.isValid() || url.scheme() != QStringLiteral("https") || url.host().isEmpty()
        || !url.userInfo().isEmpty() || url.hasQuery() || url.hasFragment()
        || (!url.path().isEmpty() && url.path() != QStringLiteral("/"))) {
        return std::nullopt;
    }
    QUrl origin;
    origin.setScheme(QStringLiteral("https"));
    origin.setHost(url.host());
    if (url.port() != -1) origin.setPort(url.port());
    return origin.toString(QUrl::FullyEncoded | QUrl::RemovePath | QUrl::StripTrailingSlash);
}

} // namespace

DaemonLauncher::DaemonLauncher(QString applicationDirectory, QString dataDirectory,
                               QObject *parent, StartFunction starter)
    : QObject(parent), applicationDirectory_(std::move(applicationDirectory)),
      dataDirectory_(std::move(dataDirectory)), starter_(std::move(starter))
{
    if (!starter_) {
        starter_ = [](const QString &program, const QStringList &arguments,
                      const QString &workingDirectory, qint64 *pid) {
            return QProcess::startDetached(program, arguments, workingDirectory, pid);
        };
    }
    guardTimer_.setParent(this);
    guardTimer_.setSingleShot(true);
    guardTimer_.setInterval(15000);
    connect(&guardTimer_, &QTimer::timeout, this, [this]() {
        busy_ = false;
        emit busyChanged(false);
        emit launchFinished(false, QStringLiteral("后台服务未在预期时间内就绪，请检查设置后重试"));
    });
}

void DaemonLauncher::startDaemon(const QString &serverOrigin, const QString &deviceName)
{
    if (busy_) return;
    busy_ = true;
    emit busyChanged(true);
    QString validationError;
    const auto spec = buildLaunchSpec(applicationDirectory_, dataDirectory_, serverOrigin,
                                      deviceName, &validationError);
    if (!spec) {
        busy_ = false;
        emit busyChanged(false);
        emit launchFinished(false, validationError);
        return;
    }
    qint64 pid = 0;
    const bool started = starter_(spec->program, spec->arguments, spec->workingDirectory, &pid);
    if (!started || pid <= 0) {
        busy_ = false;
        emit busyChanged(false);
        emit launchFinished(false, QStringLiteral("后台服务启动失败"));
        return;
    }
    guardTimer_.start();
    emit launchFinished(true, QStringLiteral("后台服务正在启动"));
}

void DaemonLauncher::setDaemonAvailable(bool available)
{
    if (!available || !busy_) return;
    guardTimer_.stop();
    busy_ = false;
    emit busyChanged(false);
    emit launchFinished(true, QStringLiteral("后台服务已就绪"));
}

std::optional<LaunchSpec> DaemonLauncher::buildLaunchSpec(const QString &applicationDirectory,
                                                          const QString &dataDirectory,
                                                          const QString &serverOrigin,
                                                          const QString &deviceName,
                                                          QString *error)
{
    const QFileInfo appDirectoryInfo(applicationDirectory);
    const QString canonicalApplicationDirectory = appDirectoryInfo.canonicalFilePath();
    const QFileInfo daemonInfo(QDir(applicationDirectory).filePath(QStringLiteral("aisummoner-client")));
    const QString canonicalDaemonPath = daemonInfo.canonicalFilePath();
    const QFileInfo canonicalDaemonInfo(canonicalDaemonPath);
    if (canonicalApplicationDirectory.isEmpty() || !daemonInfo.exists() || !daemonInfo.isFile()
        || daemonInfo.isSymLink() || !daemonInfo.isExecutable() || canonicalDaemonPath.isEmpty()
        || canonicalDaemonInfo.canonicalPath() != canonicalApplicationDirectory
        || daemonInfo.permissions().testFlag(QFileDevice::WriteGroup)
        || daemonInfo.permissions().testFlag(QFileDevice::WriteOther)) {
        if (error) *error = QStringLiteral("未找到可信的后台服务程序");
        return std::nullopt;
    }
    if (!QDir::isAbsolutePath(dataDirectory) || QDir::cleanPath(dataDirectory) != dataDirectory) {
        if (error) *error = QStringLiteral("本机数据目录无效");
        return std::nullopt;
    }
    const QString homeDirectory = QDir::cleanPath(QDir::homePath());
    if (!QDir::isAbsolutePath(homeDirectory)) {
        if (error) *error = QStringLiteral("本机用户目录无效");
        return std::nullopt;
    }
    const auto origin = canonicalHttpsOrigin(serverOrigin);
    if (!origin) {
        if (error) *error = QStringLiteral("Server 地址必须是有效的 HTTPS Origin");
        return std::nullopt;
    }
    if (!deviceName.isEmpty() && !safeDeviceName(deviceName)) {
        if (error) *error = QStringLiteral("设备名称无效（最多 128 字节）");
        return std::nullopt;
    }
    LaunchSpec spec;
    spec.program = canonicalDaemonPath;
    // An AppImage is mounted at a transient path.  A long-lived detached
    // daemon must not retain that mount as its current working directory after
    // the GUI exits, so use the stable user home instead.
    spec.workingDirectory = homeDirectory;
    spec.arguments = {QStringLiteral("daemon"), QStringLiteral("--server"), *origin,
                      QStringLiteral("--data-dir"), dataDirectory};
    if (!deviceName.isEmpty()) {
        spec.arguments << QStringLiteral("--name") << deviceName;
    }
    return spec;
}

} // namespace aisummoner
