#include "eventmodel.h"

namespace aisummoner {

EventListModel::EventListModel(QObject *parent) : QAbstractListModel(parent) {}

int EventListModel::rowCount(const QModelIndex &parent) const
{
    return parent.isValid() ? 0 : static_cast<int>(visible_.size());
}

QVariant EventListModel::data(const QModelIndex &index, int role) const
{
    if (!index.isValid() || index.row() < 0 || index.row() >= visible_.size()) return {};
    const RemoteEvent &event = events_.at(visible_.at(index.row()));
    switch (role) {
    case Qt::DisplayRole:
        return QStringLiteral("%1   %2")
            .arg(event.at.toLocalTime().toString(QStringLiteral("MM-dd HH:mm:ss")), event.summary);
    case SequenceRole: return QVariant::fromValue(event.sequence);
    case TimestampRole: return event.at;
    case KindRole: return event.kind;
    case LevelRole:
        if (event.level == EventLevel::Warning) return QStringLiteral("warning");
        if (event.level == EventLevel::Error) return QStringLiteral("error");
        return QStringLiteral("info");
    case CategoryRole:
        return event.category == EventCategory::Control ? QStringLiteral("control")
                                                        : QStringLiteral("connection");
    case SummaryRole: return event.summary;
    default: return {};
    }
}

QHash<int, QByteArray> EventListModel::roleNames() const
{
    return {{SequenceRole, "sequence"}, {TimestampRole, "timestamp"}, {KindRole, "kind"},
            {LevelRole, "level"}, {CategoryRole, "category"}, {SummaryRole, "summary"}};
}

void EventListModel::append(const QVector<RemoteEvent> &events)
{
    if (events.isEmpty()) return;
    quint64 last = events_.isEmpty() ? 0 : events_.constLast().sequence;
    for (const RemoteEvent &event : events) {
        if (event.sequence <= last) continue;
        events_.push_back(event);
        last = event.sequence;
    }
    if (events_.size() > 200) {
        events_.erase(events_.begin(), events_.begin() + (events_.size() - 200));
    }
    rebuildVisible();
}

void EventListModel::setFilter(Filter filter)
{
    if (filter_ == filter) return;
    filter_ = filter;
    rebuildVisible();
}

void EventListModel::clear()
{
    if (events_.isEmpty()) return;
    beginResetModel();
    events_.clear();
    visible_.clear();
    endResetModel();
}

void EventListModel::rebuildVisible()
{
    beginResetModel();
    visible_.clear();
    for (int index = 0; index < events_.size(); ++index) {
        const RemoteEvent &event = events_.at(index);
        if (filter_ == Filter::All
            || (filter_ == Filter::Connection && event.category == EventCategory::Connection)
            || (filter_ == Filter::Control && event.category == EventCategory::Control)) {
            visible_.push_back(index);
        }
    }
    endResetModel();
}

} // namespace aisummoner
