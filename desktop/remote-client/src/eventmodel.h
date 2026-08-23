#pragma once

#include "models.h"

#include <QAbstractListModel>

namespace aisummoner {

class EventListModel final : public QAbstractListModel {
    Q_OBJECT

public:
    enum class Filter { All, Connection, Control };
    Q_ENUM(Filter)
    enum Roles {
        SequenceRole = Qt::UserRole + 1,
        TimestampRole,
        KindRole,
        LevelRole,
        CategoryRole,
        SummaryRole,
    };

    explicit EventListModel(QObject *parent = nullptr);

    int rowCount(const QModelIndex &parent = {}) const override;
    QVariant data(const QModelIndex &index, int role = Qt::DisplayRole) const override;
    QHash<int, QByteArray> roleNames() const override;

    void append(const QVector<RemoteEvent> &events);
    void clear();
    void setFilter(Filter filter);
    Filter filter() const { return filter_; }
    QVector<RemoteEvent> allEvents() const { return events_; }

private:
    void rebuildVisible();

    QVector<RemoteEvent> events_;
    QVector<int> visible_;
    Filter filter_ = Filter::All;
};

} // namespace aisummoner
