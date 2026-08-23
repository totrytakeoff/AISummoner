#include "strictjson.h"

#include <QJsonArray>
#include <QJsonDocument>
#include <QSet>

namespace aisummoner {
namespace {

class Scanner {
public:
    explicit Scanner(const QByteArray &input) : input_(input) {}

    bool scan(QString *error)
    {
        skipWhitespace();
        if (!scanValue(error)) {
            return false;
        }
        skipWhitespace();
        if (position_ != input_.size()) {
            return fail(error);
        }
        return true;
    }

private:
    bool scanValue(QString *error)
    {
        skipWhitespace();
        if (position_ >= input_.size()) {
            return fail(error);
        }
        switch (input_.at(position_)) {
        case '{':
            return scanObject(error);
        case '[':
            return scanArray(error);
        case '"':
            return scanString(nullptr, error);
        default:
            return scanPrimitive(error);
        }
    }

    bool scanObject(QString *error)
    {
        ++position_;
        skipWhitespace();
        QSet<QString> keys;
        if (consume('}')) {
            return true;
        }
        for (;;) {
            QByteArray encodedKey;
            if (!scanString(&encodedKey, error)) {
                return false;
            }
            QJsonParseError parseError;
            const auto decoded = QJsonDocument::fromJson("[" + encodedKey + "]", &parseError);
            if (parseError.error != QJsonParseError::NoError || !decoded.isArray()
                || decoded.array().size() != 1 || !decoded.array().at(0).isString()) {
                return fail(error);
            }
            const QString key = decoded.array().at(0).toString();
            if (keys.contains(key)) {
                if (error) {
                    *error = QStringLiteral("duplicate JSON object key");
                }
                return false;
            }
            keys.insert(key);
            skipWhitespace();
            if (!consume(':') || !scanValue(error)) {
                return fail(error);
            }
            skipWhitespace();
            if (consume('}')) {
                return true;
            }
            if (!consume(',')) {
                return fail(error);
            }
            skipWhitespace();
        }
    }

    bool scanArray(QString *error)
    {
        ++position_;
        skipWhitespace();
        if (consume(']')) {
            return true;
        }
        for (;;) {
            if (!scanValue(error)) {
                return false;
            }
            skipWhitespace();
            if (consume(']')) {
                return true;
            }
            if (!consume(',')) {
                return fail(error);
            }
            skipWhitespace();
        }
    }

    bool scanString(QByteArray *encoded, QString *error)
    {
        if (position_ >= input_.size() || input_.at(position_) != '"') {
            return fail(error);
        }
        const qsizetype start = position_++;
        while (position_ < input_.size()) {
            const char current = input_.at(position_++);
            if (current == '"') {
                if (encoded) {
                    *encoded = input_.mid(start, position_ - start);
                }
                return true;
            }
            if (static_cast<unsigned char>(current) < 0x20) {
                return fail(error);
            }
            if (current != '\\') {
                continue;
            }
            if (position_ >= input_.size()) {
                return fail(error);
            }
            const char escaped = input_.at(position_++);
            if (escaped == 'u') {
                for (int i = 0; i < 4; ++i) {
                    if (position_ >= input_.size()) {
                        return fail(error);
                    }
                    const char hex = input_.at(position_++);
                    if (!isHex(hex)) {
                        return fail(error);
                    }
                }
            } else if (!QByteArray("\"\\/bfnrt").contains(escaped)) {
                return fail(error);
            }
        }
        return fail(error);
    }

    bool scanPrimitive(QString *error)
    {
        const qsizetype start = position_;
        while (position_ < input_.size()) {
            const char current = input_.at(position_);
            if (current == ',' || current == ']' || current == '}'
                || current == ' ' || current == '\t' || current == '\r' || current == '\n') {
                break;
            }
            ++position_;
        }
        return position_ > start || fail(error);
    }

    void skipWhitespace()
    {
        while (position_ < input_.size()) {
            const char current = input_.at(position_);
            if (current != ' ' && current != '\t' && current != '\r' && current != '\n') {
                break;
            }
            ++position_;
        }
    }

    bool consume(char expected)
    {
        if (position_ >= input_.size() || input_.at(position_) != expected) {
            return false;
        }
        ++position_;
        return true;
    }

    static bool isHex(char value)
    {
        return (value >= '0' && value <= '9') || (value >= 'a' && value <= 'f')
            || (value >= 'A' && value <= 'F');
    }

    static bool fail(QString *error)
    {
        if (error && error->isEmpty()) {
            *error = QStringLiteral("invalid JSON");
        }
        return false;
    }

    const QByteArray &input_;
    qsizetype position_ = 0;
};

} // namespace

bool rejectDuplicateJsonKeys(const QByteArray &json, QString *error)
{
    if (error) {
        error->clear();
    }
    return Scanner(json).scan(error);
}

} // namespace aisummoner
