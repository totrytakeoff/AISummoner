#pragma once

#include "appsettings.h"
#include "models.h"

#include <QMainWindow>
#include <QWidget>

class QComboBox;
class QFrame;
class QLabel;
class QLineEdit;
class QListView;
class QListWidget;
class QProgressBar;
class QPushButton;
class QStackedWidget;
class QTimer;

namespace aisummoner {

class DaemonClient;
class DaemonLauncher;
class EventListModel;

class StatusPage final : public QWidget {
    Q_OBJECT

public:
    explicit StatusPage(QWidget *parent = nullptr);

    void setAvailable(bool available);
    void setStatus(const RemoteStatus &status);
    void setActionPending(bool pending);
    void setActionMessage(const QString &message, bool error);
    void setRecentEvents(const QVector<RemoteEvent> &events);

signals:
    void pauseRequested();
    void resumeRequested();
    void refreshRequested();
    void startRequested();

private:
    void updateCountdown();
    void copyDeviceId();
    void copyPairingCode();
    void showCopyFeedback(QPushButton *button, const QString &normalText);

    bool available_ = false;
    bool actionPending_ = false;
    RemoteStatus status_;
    QString visiblePairingCode_;
    QDateTime pairingExpiry_;

    QLabel *statusPill_ = nullptr;
    QLabel *statusDetail_ = nullptr;
    QLabel *deviceName_ = nullptr;
    QLabel *deviceId_ = nullptr;
    QLabel *serverOrigin_ = nullptr;
    QLabel *pairingCode_ = nullptr;
    QLabel *pairingCountdown_ = nullptr;
    QLabel *connectionValue_ = nullptr;
    QLabel *sessionCount_ = nullptr;
    QLabel *actionMessage_ = nullptr;
    QFrame *pairingCard_ = nullptr;
    QProgressBar *pairingProgress_ = nullptr;
    QPushButton *deviceCopyButton_ = nullptr;
    QPushButton *pairingCopyButton_ = nullptr;
    QPushButton *refreshButton_ = nullptr;
    QPushButton *primaryButton_ = nullptr;
    QPushButton *startButton_ = nullptr;
    QListWidget *recentEvents_ = nullptr;
    QTimer *countdownTimer_ = nullptr;
};

class EventsPage final : public QWidget {
    Q_OBJECT

public:
    explicit EventsPage(EventListModel *model, QWidget *parent = nullptr);

private:
    EventListModel *model_;
    QComboBox *filter_ = nullptr;
    QListView *list_ = nullptr;
};

class SettingsPage final : public QWidget {
    Q_OBJECT

public:
    explicit SettingsPage(QWidget *parent = nullptr);

    void setPreferences(const UserPreferences &preferences);
    UserPreferences preferences() const;
    void setDaemonAvailable(bool available);
    void setLauncherBusy(bool busy);
    void setMessage(const QString &message, bool error = false);

signals:
    void saveRequested(const aisummoner::UserPreferences &preferences);
    void startRequested();

private:
    QLineEdit *serverOrigin_ = nullptr;
    QLineEdit *deviceName_ = nullptr;
    QComboBox *theme_ = nullptr;
    QLabel *message_ = nullptr;
    QLabel *daemonState_ = nullptr;
    QPushButton *saveButton_ = nullptr;
    QPushButton *startButton_ = nullptr;
    bool daemonAvailable_ = false;
};

class MainWindow final : public QMainWindow {
    Q_OBJECT

public:
    MainWindow(AppSettings *settings, DaemonClient *client, DaemonLauncher *launcher,
               QWidget *parent = nullptr);

    EventListModel *eventModel() const { return eventModel_; }
    StatusPage *statusPage() const { return statusPage_; }
    SettingsPage *settingsPage() const { return settingsPage_; }

protected:
    void closeEvent(QCloseEvent *event) override;

private:
    void requestDaemonStart();
    void savePreferences(const UserPreferences &preferences);
    void selectPage(int index);

    AppSettings *settings_;
    DaemonClient *client_;
    DaemonLauncher *launcher_;
    UserPreferences preferences_;
    EventListModel *eventModel_ = nullptr;
    StatusPage *statusPage_ = nullptr;
    EventsPage *eventsPage_ = nullptr;
    SettingsPage *settingsPage_ = nullptr;
    QStackedWidget *pages_ = nullptr;
    QVector<QPushButton *> navigationButtons_;
};

} // namespace aisummoner

Q_DECLARE_METATYPE(aisummoner::UserPreferences)
