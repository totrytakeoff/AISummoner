#include "mainwindow.h"

#include "daemonclient.h"
#include "daemonlauncher.h"
#include "eventmodel.h"
#include "theme.h"

#include <QApplication>
#include <QButtonGroup>
#include <QClipboard>
#include <QCloseEvent>
#include <QComboBox>
#include <QDateTime>
#include <QFrame>
#include <QGuiApplication>
#include <QHBoxLayout>
#include <QLabel>
#include <QLineEdit>
#include <QListView>
#include <QListWidget>
#include <QMessageBox>
#include <QProgressBar>
#include <QPushButton>
#include <QScrollArea>
#include <QStackedWidget>
#include <QTimer>
#include <QVBoxLayout>

namespace aisummoner {
namespace {

QFrame *card(QWidget *parent = nullptr)
{
    auto *frame = new QFrame(parent);
    frame->setProperty("card", true);
    return frame;
}

QLabel *secondaryLabel(const QString &text = {}, QWidget *parent = nullptr)
{
    auto *label = new QLabel(text, parent);
    label->setProperty("secondary", true);
    label->setWordWrap(true);
    return label;
}

QWidget *scrollablePage(QWidget *contents, QWidget *parent)
{
    auto *scroll = new QScrollArea(parent);
    scroll->setWidgetResizable(true);
    scroll->setHorizontalScrollBarPolicy(Qt::ScrollBarAlwaysOff);
    scroll->setWidget(contents);
    auto *wrapper = new QWidget(parent);
    auto *layout = new QVBoxLayout(wrapper);
    layout->setContentsMargins(0, 0, 0, 0);
    layout->addWidget(scroll);
    return wrapper;
}

QString countdownText(qint64 seconds)
{
    if (seconds <= 0) return QStringLiteral("已过期");
    const qint64 minutes = seconds / 60;
    const qint64 remainder = seconds % 60;
    return QStringLiteral("%1:%2 后过期").arg(minutes, 2, 10, QLatin1Char('0'))
        .arg(remainder, 2, 10, QLatin1Char('0'));
}

} // namespace

StatusPage::StatusPage(QWidget *parent) : QWidget(parent)
{
    setObjectName(QStringLiteral("StatusPage"));
    auto *contents = new QWidget;
    auto *body = new QVBoxLayout(contents);
    body->setContentsMargins(24, 24, 24, 24);
    body->setSpacing(16);

    auto *titleRow = new QHBoxLayout;
    auto *title = new QLabel(QStringLiteral("设备状态"));
    title->setObjectName(QStringLiteral("PageTitle"));
    statusPill_ = new QLabel(QStringLiteral("● 后台服务不可用"));
    statusPill_->setObjectName(QStringLiteral("StatusPill"));
    statusPill_->setAccessibleName(QStringLiteral("设备连接状态"));
    titleRow->addWidget(title);
    titleRow->addStretch();
    titleRow->addWidget(statusPill_);
    body->addLayout(titleRow);

    statusDetail_ = secondaryLabel(QStringLiteral("启动本机后台服务后即可连接控制端。"));
    statusDetail_->setObjectName(QStringLiteral("StatusDetail"));
    body->addWidget(statusDetail_);

    auto *deviceCard = card();
    deviceCard->setObjectName(QStringLiteral("DeviceCard"));
    auto *deviceLayout = new QVBoxLayout(deviceCard);
    deviceLayout->setContentsMargins(20, 18, 20, 18);
    auto *deviceTop = new QHBoxLayout;
    deviceName_ = new QLabel(QStringLiteral("本机设备"));
    deviceName_->setObjectName(QStringLiteral("SectionTitle"));
    deviceCopyButton_ = new QPushButton(QStringLiteral("复制 ID"));
    deviceCopyButton_->setObjectName(QStringLiteral("CopyDeviceIdButton"));
    deviceCopyButton_->setAccessibleName(QStringLiteral("复制设备 ID"));
    deviceCopyButton_->setEnabled(false);
    deviceTop->addWidget(deviceName_);
    deviceTop->addStretch();
    deviceTop->addWidget(deviceCopyButton_);
    deviceId_ = secondaryLabel(QStringLiteral("Device ID 将在后台服务启动后显示"));
    deviceId_->setObjectName(QStringLiteral("DeviceIdLabel"));
    deviceId_->setTextInteractionFlags(Qt::TextSelectableByKeyboard | Qt::TextSelectableByMouse);
    serverOrigin_ = secondaryLabel(QStringLiteral("Server：—"));
    serverOrigin_->setObjectName(QStringLiteral("ServerOriginLabel"));
    serverOrigin_->setTextInteractionFlags(Qt::TextSelectableByKeyboard | Qt::TextSelectableByMouse);
    deviceLayout->addLayout(deviceTop);
    deviceLayout->addWidget(deviceId_);
    deviceLayout->addWidget(serverOrigin_);
    body->addWidget(deviceCard);

    pairingCard_ = card();
    pairingCard_->setObjectName(QStringLiteral("PairingCard"));
    auto *pairingLayout = new QVBoxLayout(pairingCard_);
    pairingLayout->setContentsMargins(20, 18, 20, 18);
    auto *pairingTitle = new QLabel(QStringLiteral("连接此设备"));
    pairingTitle->setObjectName(QStringLiteral("SectionTitle"));
    pairingCode_ = new QLabel;
    pairingCode_->setObjectName(QStringLiteral("PairingCode"));
    pairingCode_->setAccessibleName(QStringLiteral("当前配对码"));
    pairingCode_->setAlignment(Qt::AlignCenter);
    pairingCountdown_ = secondaryLabel();
    pairingCountdown_->setObjectName(QStringLiteral("PairingCountdown"));
    pairingCountdown_->setAlignment(Qt::AlignCenter);
    pairingProgress_ = new QProgressBar;
    pairingProgress_->setObjectName(QStringLiteral("PairingProgress"));
    pairingProgress_->setTextVisible(false);
    pairingProgress_->setRange(0, 600);
    auto *pairingActions = new QHBoxLayout;
    pairingCopyButton_ = new QPushButton(QStringLiteral("复制配对码"));
    pairingCopyButton_->setObjectName(QStringLiteral("CopyPairingButton"));
    pairingCopyButton_->setAccessibleName(QStringLiteral("复制配对码"));
    refreshButton_ = new QPushButton(QStringLiteral("刷新"));
    refreshButton_->setObjectName(QStringLiteral("RefreshPairingButton"));
    refreshButton_->setAccessibleName(QStringLiteral("刷新配对码"));
    pairingActions->addStretch();
    pairingActions->addWidget(pairingCopyButton_);
    pairingActions->addWidget(refreshButton_);
    pairingLayout->addWidget(pairingTitle);
    pairingLayout->addWidget(pairingCode_);
    pairingLayout->addWidget(pairingCountdown_);
    pairingLayout->addWidget(pairingProgress_);
    pairingLayout->addLayout(pairingActions);
    pairingCard_->hide();
    body->addWidget(pairingCard_);

    auto *metrics = new QHBoxLayout;
    metrics->setSpacing(16);
    auto *connectionCard = card();
    auto *connectionLayout = new QVBoxLayout(connectionCard);
    connectionLayout->setContentsMargins(20, 16, 20, 16);
    connectionLayout->addWidget(secondaryLabel(QStringLiteral("连接")));
    connectionValue_ = new QLabel(QStringLiteral("本机后台服务"));
    connectionValue_->setObjectName(QStringLiteral("SectionTitle"));
    connectionValue_->setAccessibleName(QStringLiteral("后台连接阶段"));
    connectionLayout->addWidget(connectionValue_);
    auto *sessionCard = card();
    auto *sessionLayout = new QVBoxLayout(sessionCard);
    sessionLayout->setContentsMargins(20, 16, 20, 16);
    sessionLayout->addWidget(secondaryLabel(QStringLiteral("当前控制会话")));
    sessionCount_ = new QLabel(QStringLiteral("—"));
    sessionCount_->setObjectName(QStringLiteral("SessionCount"));
    sessionCount_->setAccessibleName(QStringLiteral("当前控制会话总数"));
    sessionLayout->addWidget(sessionCount_);
    metrics->addWidget(connectionCard, 1);
    metrics->addWidget(sessionCard, 1);
    body->addLayout(metrics);

    auto *recentCard = card();
    auto *recentLayout = new QVBoxLayout(recentCard);
    recentLayout->setContentsMargins(20, 16, 20, 16);
    auto *recentTitle = new QLabel(QStringLiteral("最近事件"));
    recentTitle->setObjectName(QStringLiteral("SectionTitle"));
    recentEvents_ = new QListWidget;
    recentEvents_->setObjectName(QStringLiteral("RecentEvents"));
    recentEvents_->setFocusPolicy(Qt::NoFocus);
    recentEvents_->setSelectionMode(QAbstractItemView::NoSelection);
    recentEvents_->setMaximumHeight(132);
    recentLayout->addWidget(recentTitle);
    recentLayout->addWidget(recentEvents_);
    body->addWidget(recentCard);

    actionMessage_ = secondaryLabel();
    actionMessage_->setObjectName(QStringLiteral("ActionMessage"));
    actionMessage_->hide();
    body->addStretch();

    auto *outer = new QVBoxLayout(this);
    outer->setContentsMargins(0, 0, 0, 0);
    outer->setSpacing(0);
    outer->addWidget(scrollablePage(contents, this), 1);
    auto *footer = new QWidget;
    footer->setObjectName(QStringLiteral("StatusFooter"));
    auto *footerLayout = new QHBoxLayout(footer);
    footerLayout->setContentsMargins(24, 10, 24, 12);
    footerLayout->addWidget(actionMessage_, 1);
    auto *actions = new QHBoxLayout;
    startButton_ = new QPushButton(QStringLiteral("启动后台服务"));
    startButton_->setObjectName(QStringLiteral("StartDaemonButton"));
    startButton_->setProperty("primary", true);
    startButton_->setAccessibleName(QStringLiteral("启动本机后台服务"));
    primaryButton_ = new QPushButton(QStringLiteral("暂停连接"));
    primaryButton_->setObjectName(QStringLiteral("PrimaryDaemonAction"));
    primaryButton_->setProperty("primary", true);
    primaryButton_->setAccessibleName(QStringLiteral("暂停远程控制连接"));
    primaryButton_->hide();
    actions->addWidget(startButton_);
    actions->addWidget(primaryButton_);
    footerLayout->addLayout(actions);
    outer->addWidget(footer);

    countdownTimer_ = new QTimer(this);
    countdownTimer_->setInterval(1000);
    connect(countdownTimer_, &QTimer::timeout, this, &StatusPage::updateCountdown);
    connect(deviceCopyButton_, &QPushButton::clicked, this, &StatusPage::copyDeviceId);
    connect(pairingCopyButton_, &QPushButton::clicked, this, &StatusPage::copyPairingCode);
    connect(refreshButton_, &QPushButton::clicked, this, &StatusPage::refreshRequested);
    connect(startButton_, &QPushButton::clicked, this, &StatusPage::startRequested);
    connect(primaryButton_, &QPushButton::clicked, this, [this]() {
        if (status_.phase == DaemonPhase::Paused) emit resumeRequested();
        else emit pauseRequested();
    });
}

void StatusPage::setAvailable(bool available)
{
    available_ = available;
    if (!available_) {
        statusPill_->setText(QStringLiteral("○ 后台服务不可用"));
        statusDetail_->setText(QStringLiteral("设置 Server 地址后启动本机后台服务。"));
        sessionCount_->setText(QStringLiteral("—"));
        connectionValue_->setText(QStringLiteral("不可用"));
        pairingCard_->hide();
        visiblePairingCode_.clear();
        countdownTimer_->stop();
        primaryButton_->hide();
        startButton_->show();
        deviceCopyButton_->setEnabled(false);
    } else {
        startButton_->hide();
        primaryButton_->show();
    }
}

void StatusPage::setStatus(const RemoteStatus &status)
{
    status_ = status;
    setAvailable(true);
    QString label = phaseLabel(status.phase);
    if (status.phase == DaemonPhase::Online && status.pairing && !status.pairing->expired) {
        label = QStringLiteral("等待配对");
    }
    statusPill_->setText(QStringLiteral("● %1").arg(label));
    statusDetail_->setText(status.phase == DaemonPhase::Online
        ? QStringLiteral("后台连接正常；只有已配对的控制端可以建立控制会话。")
        : status.phase == DaemonPhase::Retrying
            ? QStringLiteral("连接暂时中断，后台服务会自动重试。")
            : status.phase == DaemonPhase::Paused
                ? QStringLiteral("远程控制已暂停，现有控制会话已经关闭。")
                : QStringLiteral("后台服务状态：%1").arg(label));
    deviceName_->setText(status.deviceName);
    deviceId_->setText(status.deviceId);
    serverOrigin_->setText(QStringLiteral("Server：%1").arg(status.serverOrigin));
    deviceCopyButton_->setEnabled(!status.deviceId.isEmpty());
    sessionCount_->setText(QString::number(status.activeSessions));
    connectionValue_->setText(phaseLabel(status.phase));
    primaryButton_->setText(status.phase == DaemonPhase::Paused ? QStringLiteral("恢复连接")
                                                                : QStringLiteral("暂停连接"));
    primaryButton_->setAccessibleName(status.phase == DaemonPhase::Paused
        ? QStringLiteral("恢复远程控制连接") : QStringLiteral("暂停远程控制连接"));

    const bool showPairing = status.pairing.has_value() && status.phase != DaemonPhase::Paused;
    pairingCard_->setVisible(showPairing);
    if (showPairing) {
        visiblePairingCode_ = status.pairing->expired ? QString() : status.pairing->code;
        pairingExpiry_ = status.pairing->expiresAt;
        pairingCode_->setText(visiblePairingCode_.isEmpty() ? QStringLiteral("配对码已过期")
                                                            : visiblePairingCode_);
        pairingCopyButton_->setEnabled(!visiblePairingCode_.isEmpty());
        refreshButton_->setEnabled(!actionPending_);
        countdownTimer_->start();
        updateCountdown();
    } else {
        visiblePairingCode_.clear();
        countdownTimer_->stop();
    }
}

void StatusPage::setActionPending(bool pending)
{
    actionPending_ = pending;
    primaryButton_->setEnabled(!pending);
    refreshButton_->setEnabled(!pending && !pairingCard_->isHidden());
    startButton_->setEnabled(!pending);
    if (pending) setActionMessage(QStringLiteral("正在完成操作…"), false);
}

void StatusPage::setActionMessage(const QString &message, bool error)
{
    actionMessage_->setText(message);
    actionMessage_->setObjectName(error ? QStringLiteral("errorText") : QStringLiteral("ActionMessage"));
    actionMessage_->setVisible(!message.isEmpty());
    actionMessage_->style()->unpolish(actionMessage_);
    actionMessage_->style()->polish(actionMessage_);
}

void StatusPage::setRecentEvents(const QVector<RemoteEvent> &events)
{
    recentEvents_->clear();
    const qsizetype first = qMax(qsizetype{0}, events.size() - 3);
    for (qsizetype index = events.size(); index > first; --index) {
        const RemoteEvent &event = events.at(index - 1);
        recentEvents_->addItem(QStringLiteral("%1  %2")
            .arg(event.at.toLocalTime().toString(QStringLiteral("HH:mm:ss")), event.summary));
    }
    if (recentEvents_->count() == 0) recentEvents_->addItem(QStringLiteral("暂无事件"));
}

void StatusPage::updateCountdown()
{
    if (pairingCard_->isHidden()) return;
    qint64 seconds = QDateTime::currentDateTimeUtc().secsTo(pairingExpiry_);
    if (seconds <= 0) {
        seconds = 0;
        visiblePairingCode_.clear();
        pairingCode_->setText(QStringLiteral("配对码已过期"));
        pairingCopyButton_->setEnabled(false);
        countdownTimer_->stop();
    }
    pairingCountdown_->setText(countdownText(seconds));
    pairingProgress_->setMaximum(qMax(1, pairingProgress_->maximum()));
    pairingProgress_->setValue(qMin(pairingProgress_->maximum(), static_cast<int>(seconds)));
}

void StatusPage::copyDeviceId()
{
    if (status_.deviceId.isEmpty()) return;
    QGuiApplication::clipboard()->setText(status_.deviceId);
    showCopyFeedback(deviceCopyButton_, QStringLiteral("复制 ID"));
}

void StatusPage::copyPairingCode()
{
    if (visiblePairingCode_.isEmpty()) return;
    QGuiApplication::clipboard()->setText(visiblePairingCode_);
    showCopyFeedback(pairingCopyButton_, QStringLiteral("复制配对码"));
}

void StatusPage::showCopyFeedback(QPushButton *button, const QString &normalText)
{
    button->setText(QStringLiteral("已复制"));
    QTimer::singleShot(1500, button, [button, normalText]() { button->setText(normalText); });
}

EventsPage::EventsPage(EventListModel *model, QWidget *parent) : QWidget(parent), model_(model)
{
    setObjectName(QStringLiteral("EventsPage"));
    auto *layout = new QVBoxLayout(this);
    layout->setContentsMargins(24, 24, 24, 24);
    layout->setSpacing(16);
    auto *top = new QHBoxLayout;
    auto *title = new QLabel(QStringLiteral("活动事件"));
    title->setObjectName(QStringLiteral("PageTitle"));
    filter_ = new QComboBox;
    filter_->setObjectName(QStringLiteral("EventFilter"));
    filter_->setAccessibleName(QStringLiteral("事件类型筛选"));
    filter_->addItems({QStringLiteral("全部"), QStringLiteral("连接"), QStringLiteral("控制")});
    top->addWidget(title);
    top->addStretch();
    top->addWidget(filter_);
    layout->addLayout(top);
    layout->addWidget(secondaryLabel(QStringLiteral("仅显示后台服务提供的固定、脱敏摘要。最多保留 200 条。")));
    list_ = new QListView;
    list_->setObjectName(QStringLiteral("EventList"));
    list_->setAccessibleName(QStringLiteral("远程控制活动事件"));
    list_->setModel(model_);
    list_->setUniformItemSizes(true);
    layout->addWidget(list_, 1);
    connect(filter_, &QComboBox::currentIndexChanged, this, [this](int index) {
        if (index == 1) model_->setFilter(EventListModel::Filter::Connection);
        else if (index == 2) model_->setFilter(EventListModel::Filter::Control);
        else model_->setFilter(EventListModel::Filter::All);
    });
}

SettingsPage::SettingsPage(QWidget *parent) : QWidget(parent)
{
    setObjectName(QStringLiteral("SettingsPage"));
    auto *contents = new QWidget;
    auto *layout = new QVBoxLayout(contents);
    layout->setContentsMargins(24, 24, 24, 24);
    layout->setSpacing(16);
    auto *title = new QLabel(QStringLiteral("设置"));
    title->setObjectName(QStringLiteral("PageTitle"));
    layout->addWidget(title);

    auto *connectionCard = card();
    auto *connection = new QVBoxLayout(connectionCard);
    connection->setContentsMargins(20, 18, 20, 18);
    auto *connectionTitle = new QLabel(QStringLiteral("连接设置"));
    connectionTitle->setObjectName(QStringLiteral("SectionTitle"));
    serverOrigin_ = new QLineEdit;
    serverOrigin_->setObjectName(QStringLiteral("ServerOriginInput"));
    serverOrigin_->setAccessibleName(QStringLiteral("AISummoner Server HTTPS 地址"));
    serverOrigin_->setPlaceholderText(QStringLiteral("https://control.example.com"));
    deviceName_ = new QLineEdit;
    deviceName_->setObjectName(QStringLiteral("DeviceNameInput"));
    deviceName_->setAccessibleName(QStringLiteral("设备显示名称"));
    deviceName_->setMaxLength(128);
    connection->addWidget(connectionTitle);
    connection->addWidget(secondaryLabel(QStringLiteral("Server HTTPS Origin")));
    connection->addWidget(serverOrigin_);
    connection->addWidget(secondaryLabel(QStringLiteral("设备显示名称（留空时使用主机名）")));
    connection->addWidget(deviceName_);
    connection->addWidget(secondaryLabel(QStringLiteral("修改会在下一次后台服务启动时生效，不会热修改正在运行的连接。")));
    layout->addWidget(connectionCard);

    auto *appearanceCard = card();
    auto *appearance = new QVBoxLayout(appearanceCard);
    appearance->setContentsMargins(20, 18, 20, 18);
    auto *appearanceTitle = new QLabel(QStringLiteral("外观"));
    appearanceTitle->setObjectName(QStringLiteral("SectionTitle"));
    theme_ = new QComboBox;
    theme_->setObjectName(QStringLiteral("ThemeInput"));
    theme_->setAccessibleName(QStringLiteral("界面主题"));
    theme_->addItems({QStringLiteral("跟随系统"), QStringLiteral("浅色"), QStringLiteral("深色")});
    appearance->addWidget(appearanceTitle);
    appearance->addWidget(theme_);
    layout->addWidget(appearanceCard);

    auto *serviceCard = card();
    auto *service = new QVBoxLayout(serviceCard);
    service->setContentsMargins(20, 18, 20, 18);
    auto *serviceTitle = new QLabel(QStringLiteral("本机后台服务"));
    serviceTitle->setObjectName(QStringLiteral("SectionTitle"));
    daemonState_ = secondaryLabel(QStringLiteral("当前不可用"));
    startButton_ = new QPushButton(QStringLiteral("启动后台服务"));
    startButton_->setObjectName(QStringLiteral("SettingsStartDaemonButton"));
    startButton_->setProperty("primary", true);
    startButton_->setAccessibleName(QStringLiteral("启动本机后台服务"));
    service->addWidget(serviceTitle);
    service->addWidget(daemonState_);
    service->addWidget(startButton_, 0, Qt::AlignLeft);
    layout->addWidget(serviceCard);

    auto *futureCard = card();
    auto *future = new QVBoxLayout(futureCard);
    future->setContentsMargins(20, 18, 20, 18);
    auto *futureTitle = new QLabel(QStringLiteral("本机权限"));
    futureTitle->setObjectName(QStringLiteral("SectionTitle"));
    future->addWidget(futureTitle);
    future->addWidget(secondaryLabel(QStringLiteral("按控制端、命令类型细分权限将在后续版本提供。当前不会显示无效开关。")));
    layout->addWidget(futureCard);

    message_ = secondaryLabel();
    message_->setObjectName(QStringLiteral("SettingsMessage"));
    message_->hide();
    layout->addWidget(message_);
    auto *actions = new QHBoxLayout;
    actions->addStretch();
    saveButton_ = new QPushButton(QStringLiteral("保存设置"));
    saveButton_->setObjectName(QStringLiteral("SaveSettingsButton"));
    saveButton_->setProperty("primary", true);
    saveButton_->setAccessibleName(QStringLiteral("保存非秘密设置"));
    actions->addWidget(saveButton_);
    layout->addLayout(actions);
    layout->addStretch();

    auto *outer = new QVBoxLayout(this);
    outer->setContentsMargins(0, 0, 0, 0);
    outer->addWidget(scrollablePage(contents, this));
    connect(saveButton_, &QPushButton::clicked, this, [this]() { emit saveRequested(preferences()); });
    connect(startButton_, &QPushButton::clicked, this, &SettingsPage::startRequested);
}

void SettingsPage::setPreferences(const UserPreferences &preferences)
{
    serverOrigin_->setText(preferences.serverOrigin);
    deviceName_->setText(preferences.deviceName);
    theme_->setCurrentIndex(preferences.theme == ThemePreference::Light ? 1
                            : preferences.theme == ThemePreference::Dark ? 2 : 0);
}

UserPreferences SettingsPage::preferences() const
{
    UserPreferences value;
    value.serverOrigin = serverOrigin_->text();
    value.deviceName = deviceName_->text();
    value.theme = theme_->currentIndex() == 1 ? ThemePreference::Light
                  : theme_->currentIndex() == 2 ? ThemePreference::Dark : ThemePreference::System;
    return value;
}

void SettingsPage::setDaemonAvailable(bool available)
{
    daemonAvailable_ = available;
    daemonState_->setText(available ? QStringLiteral("后台服务正在运行；设置将在下次启动时生效。")
                                    : QStringLiteral("后台服务未运行，可以使用已保存设置启动。"));
    startButton_->setVisible(!available);
}

void SettingsPage::setLauncherBusy(bool busy)
{
    startButton_->setEnabled(!busy && !daemonAvailable_);
    if (busy) startButton_->setText(QStringLiteral("正在启动…"));
    else startButton_->setText(QStringLiteral("启动后台服务"));
}

void SettingsPage::setMessage(const QString &message, bool error)
{
    message_->setText(message);
    message_->setObjectName(error ? QStringLiteral("errorText") : QStringLiteral("SettingsMessage"));
    message_->setVisible(!message.isEmpty());
    message_->style()->unpolish(message_);
    message_->style()->polish(message_);
}

MainWindow::MainWindow(AppSettings *settings, DaemonClient *client, DaemonLauncher *launcher,
                       QWidget *parent)
    : QMainWindow(parent), settings_(settings), client_(client), launcher_(launcher)
{
    setObjectName(QStringLiteral("RemoteClientWindow"));
    setWindowTitle(QStringLiteral("AISummoner Remote"));
    resize(940, 640);
    setMinimumSize(780, 560);

    preferences_ = settings_->load();
    auto *root = new QWidget;
    root->setObjectName(QStringLiteral("AppRoot"));
    setCentralWidget(root);
    auto *shell = new QHBoxLayout(root);
    shell->setContentsMargins(0, 0, 0, 0);
    shell->setSpacing(0);

    auto *navigation = new QWidget;
    navigation->setObjectName(QStringLiteral("NavigationRail"));
    navigation->setFixedWidth(188);
    auto *navigationLayout = new QVBoxLayout(navigation);
    navigationLayout->setContentsMargins(16, 20, 16, 20);
    navigationLayout->setSpacing(8);
    auto *brand = new QLabel(QStringLiteral("AISummoner"));
    brand->setObjectName(QStringLiteral("Brand"));
    navigationLayout->addWidget(brand);
    navigationLayout->addSpacing(20);
    auto *group = new QButtonGroup(this);
    group->setExclusive(true);
    const QStringList labels = {QStringLiteral("状态"), QStringLiteral("事件"), QStringLiteral("设置")};
    for (int index = 0; index < labels.size(); ++index) {
        auto *button = new QPushButton(labels.at(index));
        button->setObjectName(QStringLiteral("Navigation%1").arg(index));
        button->setProperty("nav", true);
        button->setCheckable(true);
        button->setAccessibleName(QStringLiteral("打开%1页面").arg(labels.at(index)));
        group->addButton(button, index);
        navigationButtons_.push_back(button);
        navigationLayout->addWidget(button);
    }
    navigationButtons_.first()->setChecked(true);
    navigationLayout->addStretch();
    auto *privacy = secondaryLabel(QStringLiteral("私钥与远程命令始终由后台服务隔离处理。"));
    navigationLayout->addWidget(privacy);
    shell->addWidget(navigation);

    eventModel_ = new EventListModel(this);
    statusPage_ = new StatusPage;
    eventsPage_ = new EventsPage(eventModel_);
    settingsPage_ = new SettingsPage;
    settingsPage_->setPreferences(preferences_);
    pages_ = new QStackedWidget;
    pages_->setObjectName(QStringLiteral("PageStack"));
    pages_->addWidget(statusPage_);
    pages_->addWidget(eventsPage_);
    pages_->addWidget(settingsPage_);
    shell->addWidget(pages_, 1);

    connect(group, &QButtonGroup::idClicked, this, &MainWindow::selectPage);
    connect(client_, &DaemonClient::availabilityChanged, this, [this](bool available) {
        statusPage_->setAvailable(available);
        settingsPage_->setDaemonAvailable(available);
        launcher_->setDaemonAvailable(available);
    });
    connect(client_, &DaemonClient::statusChanged, statusPage_, &StatusPage::setStatus);
    connect(client_, &DaemonClient::eventsReceived, this, [this](const QVector<RemoteEvent> &events) {
        eventModel_->append(events);
        statusPage_->setRecentEvents(eventModel_->allEvents());
    });
    connect(client_, &DaemonClient::eventStreamReset, this, [this]() {
        eventModel_->clear();
        statusPage_->setRecentEvents({});
    });
    connect(client_, &DaemonClient::actionStateChanged, statusPage_, &StatusPage::setActionPending);
    connect(client_, &DaemonClient::actionFinished, this,
            [this](const QString &, bool success, const QString &message) {
        statusPage_->setActionMessage(message, !success);
    });
    connect(statusPage_, &StatusPage::pauseRequested, client_, &DaemonClient::pauseDaemon);
    connect(statusPage_, &StatusPage::resumeRequested, client_, &DaemonClient::resumeDaemon);
    connect(statusPage_, &StatusPage::refreshRequested, this, [this]() {
        const auto result = QMessageBox::warning(this, QStringLiteral("刷新配对码"),
            QStringLiteral("刷新配对码会关闭当前所有控制会话。是否继续？"),
            QMessageBox::Cancel | QMessageBox::Yes, QMessageBox::Cancel);
        if (result == QMessageBox::Yes) client_->refreshPairing();
    });
    connect(statusPage_, &StatusPage::startRequested, this, &MainWindow::requestDaemonStart);
    connect(settingsPage_, &SettingsPage::startRequested, this, &MainWindow::requestDaemonStart);
    connect(settingsPage_, &SettingsPage::saveRequested, this, &MainWindow::savePreferences);
    connect(launcher_, &DaemonLauncher::busyChanged, settingsPage_, &SettingsPage::setLauncherBusy);
    connect(launcher_, &DaemonLauncher::busyChanged, statusPage_, &StatusPage::setActionPending);
    connect(launcher_, &DaemonLauncher::launchFinished, this, [this](bool success, const QString &message) {
        settingsPage_->setMessage(message, !success);
        statusPage_->setActionMessage(message, !success);
    });

    statusPage_->setAvailable(false);
    statusPage_->setRecentEvents({});
    settingsPage_->setDaemonAvailable(false);
    client_->start();
}

void MainWindow::closeEvent(QCloseEvent *event)
{
    client_->stop();
    // Deliberately no pause/stop request: closing the GUI leaves the daemon and
    // its Tunnel alive.
    QMainWindow::closeEvent(event);
}

void MainWindow::requestDaemonStart()
{
    preferences_ = settingsPage_->preferences();
    settings_->save(preferences_);
    launcher_->startDaemon(preferences_.serverOrigin, preferences_.deviceName);
}

void MainWindow::savePreferences(const UserPreferences &preferences)
{
    preferences_ = preferences;
    settings_->save(preferences_);
    applyTheme(qobject_cast<QApplication *>(QCoreApplication::instance()), preferences_.theme);
    settingsPage_->setMessage(client_->isAvailable()
        ? QStringLiteral("设置已保存，将在下一次后台服务启动时生效。")
        : QStringLiteral("设置已保存。"));
}

void MainWindow::selectPage(int index)
{
    if (index >= 0 && index < pages_->count()) pages_->setCurrentIndex(index);
}

} // namespace aisummoner
