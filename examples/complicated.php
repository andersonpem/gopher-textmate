<?php

declare(strict_types=1);

namespace Acme\SyntaxHighlighter\Test;

use ArrayAccess;
use Attribute;
use Closure;
use Countable;
use DateInterval;
use DateTimeImmutable;
use DateTimeInterface;
use Generator;
use InvalidArgumentException;
use IteratorAggregate;
use JsonSerializable;
use RuntimeException;
use Stringable;
use Throwable;
use Traversable;
use WeakMap;

use function array_filter;
use function array_map;
use function count;
use function implode;
use function is_array;
use function is_string;
use function preg_match;
use function sprintf;

use const PHP_EOL;

#[Attribute(Attribute::TARGET_CLASS | Attribute::TARGET_METHOD | Attribute::TARGET_PROPERTY | Attribute::IS_REPEATABLE)]
final readonly class Meta
{
    public function __construct(
        public string $name,
        public array $tags = [],
        public ?string $description = null,
    ) {}
}

enum TokenKind: string
{
    case Identifier = 'identifier';
    case Keyword = 'keyword';
    case Number = 'number';
    case String = 'string';
    case Operator = 'operator';
    case Comment = 'comment';
    case Whitespace = 'whitespace';
    case Unknown = 'unknown';

    public function isTrivia(): bool
    {
        return match ($this) {
            self::Comment, self::Whitespace => true,
            default => false,
        };
    }
}

interface Renderable
{
    public function render(int $indent = 0): string;
}

trait HasMetadata
{
    private array $metadata = [];

    public function setMeta(string $key, mixed $value): static
    {
        $this->metadata[$key] = $value;

        return $this;
    }

    public function getMeta(string $key, mixed $default = null): mixed
    {
        return $this->metadata[$key] ?? $default;
    }
}

#[Meta('token', tags: ['lexer', 'syntax'])]
final readonly class Token implements JsonSerializable, Stringable
{
    public function __construct(
        public TokenKind $kind,
        public string $lexeme,
        public int $line,
        public int $column,
        public array $attributes = [],
    ) {
        if ($line < 1 || $column < 1) {
            throw new InvalidArgumentException('Line and column must be positive integers.');
        }
    }

    public function __toString(): string
    {
        return sprintf('%s(%s) at %d:%d', $this->kind->value, $this->lexeme, $this->line, $this->column);
    }

    public function jsonSerialize(): array
    {
        return [
            'kind' => $this->kind->value,
            'lexeme' => $this->lexeme,
            'line' => $this->line,
            'column' => $this->column,
            'attributes' => $this->attributes,
        ];
    }
}

#[Meta('collection')]
final class TokenStream implements IteratorAggregate, Countable, ArrayAccess
{
    use HasMetadata;

    /** @var list<Token> */
    private array $tokens = [];

    public function __construct(Token ...$tokens)
    {
        $this->tokens = $tokens;
    }

    public function add(Token $token): void
    {
        $this->tokens[] = $token;
    }

    public function map(Closure $callback): array
    {
        return array_map($callback, $this->tokens);
    }

    public function filter(?TokenKind $kind = null): self
    {
        return new self(...array_filter(
            $this->tokens,
            static fn (Token $token): bool => $kind === null || $token->kind === $kind,
        ));
    }

    public function getIterator(): Traversable
    {
        yield from $this->tokens;
    }

    public function count(): int
    {
        return count($this->tokens);
    }

    public function offsetExists(mixed $offset): bool
    {
        return isset($this->tokens[$offset]);
    }

    public function offsetGet(mixed $offset): Token
    {
        return $this->tokens[$offset] ?? throw new InvalidArgumentException("Missing token at offset {$offset}.");
    }

    public function offsetSet(mixed $offset, mixed $value): void
    {
        if (!$value instanceof Token) {
            throw new InvalidArgumentException('Only Token instances are allowed.');
        }

        if ($offset === null) {
            $this->tokens[] = $value;
            return;
        }

        $this->tokens[$offset] = $value;
    }

    public function offsetUnset(mixed $offset): void
    {
        unset($this->tokens[$offset]);
    }
}

abstract class Node implements Renderable
{
    public function __construct(
        public readonly string $name,
        protected array $children = [],
    ) {}

    final public function add(Node|string|int|float|bool|null $child): static
    {
        $this->children[] = $child;

        return $this;
    }

    public function children(): Generator
    {
        foreach ($this->children as $index => $child) {
            yield $index => $child;
        }
    }

    abstract public function render(int $indent = 0): string;

    protected function spaces(int $indent): string
    {
        return str_repeat(' ', max(0, $indent));
    }
}

final class ElementNode extends Node
{
    public function __construct(
        string $name,
        private array $attributes = [],
        array $children = [],
    ) {
        parent::__construct($name, $children);
    }

    public function render(int $indent = 0): string
    {
        $prefix = $this->spaces($indent);
        $attributes = '';

        foreach ($this->attributes as $key => $value) {
            $escaped = htmlspecialchars((string) $value, ENT_QUOTES | ENT_SUBSTITUTE, 'UTF-8');
            $attributes .= " {$key}=\"{$escaped}\"";
        }

        $lines = ["{$prefix}<{$this->name}{$attributes}>"];

        foreach ($this->children() as $child) {
            $lines[] = $child instanceof Renderable
                ? $child->render($indent + 2)
                : $this->spaces($indent + 2) . htmlspecialchars((string) $child, ENT_QUOTES | ENT_SUBSTITUTE, 'UTF-8');
        }

        $lines[] = "{$prefix}</{$this->name}>";

        return implode(PHP_EOL, $lines);
    }
}

final class TextNode extends Node
{
    public function __construct(private string $text)
    {
        parent::__construct('#text');
    }

    public function render(int $indent = 0): string
    {
        return $this->spaces($indent) . htmlspecialchars($this->text, ENT_QUOTES | ENT_SUBSTITUTE, 'UTF-8');
    }
}

final class Lexer
{
    private const KEYWORDS = [
        'class' => true,
        'function' => true,
        'readonly' => true,
        'final' => true,
        'public' => true,
        'private' => true,
        'protected' => true,
        'match' => true,
        'enum' => true,
        'trait' => true,
        'interface' => true,
        'namespace' => true,
    ];

    public function tokenize(string $source): TokenStream
    {
        $stream = new TokenStream();
        $line = 1;
        $column = 1;
        $length = strlen($source);

        for ($i = 0; $i < $length; $i++) {
            $char = $source[$i];

            if ($char === "\n") {
                $stream->add(new Token(TokenKind::Whitespace, '\n', $line, $column));
                $line++;
                $column = 1;
                continue;
            }

            if (preg_match('/\s/', $char) === 1) {
                $stream->add(new Token(TokenKind::Whitespace, $char, $line, $column++));
                continue;
            }

            if (preg_match('/[a-zA-Z_]/', $char) === 1) {
                $start = $i;
                $startColumn = $column;

                while ($i + 1 < $length && preg_match('/[a-zA-Z0-9_]/', $source[$i + 1]) === 1) {
                    $i++;
                    $column++;
                }

                $word = substr($source, $start, $i - $start + 1);
                $stream->add(new Token(
                    isset(self::KEYWORDS[$word]) ? TokenKind::Keyword : TokenKind::Identifier,
                    $word,
                    $line,
                    $startColumn,
                ));
                $column++;
                continue;
            }

            if (preg_match('/[0-9]/', $char) === 1) {
                $stream->add(new Token(TokenKind::Number, $char, $line, $column++));
                continue;
            }

            $stream->add(new Token(TokenKind::Operator, $char, $line, $column++));
        }

        return $stream;
    }
}

final class SyntaxReport implements JsonSerializable
{
    public function __construct(
        public readonly DateTimeImmutable $createdAt,
        public readonly TokenStream $tokens,
        public readonly array $statistics,
    ) {}

    public static function fromSource(string $source, Lexer $lexer = new Lexer()): self
    {
        $tokens = $lexer->tokenize($source);

        $statistics = [
            'total' => count($tokens),
            'keywords' => count($tokens->filter(TokenKind::Keyword)),
            'identifiers' => count($tokens->filter(TokenKind::Identifier)),
            'trivia' => count($tokens->filter(TokenKind::Whitespace)) + count($tokens->filter(TokenKind::Comment)),
        ];

        return new self(new DateTimeImmutable('now'), $tokens, $statistics);
    }

    public function jsonSerialize(): array
    {
        return [
            'createdAt' => $this->createdAt->format(DateTimeInterface::ATOM),
            'statistics' => $this->statistics,
            'tokens' => $this->tokens->map(static fn (Token $token): array => $token->jsonSerialize()),
        ];
    }
}

final class CachePool
{
    /** @var WeakMap<object, array<string, mixed>> */
    private WeakMap $storage;

    public function __construct()
    {
        $this->storage = new WeakMap();
    }

    public function remember(object $owner, string $key, Closure $factory): mixed
    {
        $bucket = $this->storage[$owner] ?? [];

        if (!array_key_exists($key, $bucket)) {
            $bucket[$key] = $factory();
            $this->storage[$owner] = $bucket;
        }

        return $bucket[$key];
    }
}

function classify_value(mixed $value): string
{
    return match (true) {
        $value === null => 'null',
        is_string($value) => 'string',
        is_array($value) => 'array',
        $value instanceof Stringable => 'stringable',
        default => get_debug_type($value),
    };
}

function with_retry(Closure $operation, int $attempts = 3): mixed
{
    beginning:

    try {
        return $operation();
    } catch (Throwable $exception) {
        if (--$attempts > 0) {
            goto beginning;
        }

        throw new RuntimeException('Operation failed after retries.', previous: $exception);
    }
}

$complexString = <<<PHP_SAMPLE
<?php

echo "Nested PHP sample with interpolation: {\$value}";
PHP_SAMPLE;

$now = new DateTimeImmutable('2026-05-30 12:34:56 Europe/Lisbon');
$interval = new DateInterval('P1DT2H3M');

$tokens = new TokenStream(
    new Token(TokenKind::Keyword, 'final', 1, 1, ['style' => 'bold']),
    new Token(TokenKind::Keyword, 'class', 1, 7),
    new Token(TokenKind::Identifier, 'Example', 1, 13),
);

$root = new ElementNode('article', ['data-created' => $now->format(DateTimeInterface::ATOM)]);
$root
    ->add(new ElementNode('h1', children: [new TextNode('Syntax test')]))
    ->add(new ElementNode('p', ['class' => 'lead'], [
        'This file intentionally uses attributes, enums, traits, interfaces, match, closures, generators, heredoc, named arguments, union types, intersection-like behavior through interfaces, readonly properties, magic methods, and array syntax.',
    ]));

$anonymous = new class ('inline-worker') implements Renderable, Stringable {
    public function __construct(private readonly string $id) {}

    public function render(int $indent = 0): string
    {
        return str_repeat(' ', $indent) . "anonymous:{$this->id}";
    }

    public function __toString(): string
    {
        return $this->id;
    }
};

$pipeline = static function (iterable $items): Generator {
    foreach ($items as $key => $item) {
        yield $key => [
            'type' => classify_value($item),
            'value' => $item instanceof Stringable ? (string) $item : $item,
        ];
    }
};

foreach ($pipeline([$tokens, $root, $anonymous, $complexString, null, 42, 3.14, true]) as $key => $entry) {
    ${"dynamic_{$key}"} = $entry;
}

$result = with_retry(
    operation: static fn (): array => [
        'rendered' => $root->render(),
        'tokenCount' => count($tokens),
        'anonymous' => (string) $anonymous,
        'time' => $now->add($interval)->format(DateTimeInterface::ATOM),
        'sample' => $complexString,
    ],
    attempts: 1,
);

$report = SyntaxReport::fromSource($complexString);

echo json_encode(
    [
        'result' => $result,
        'report' => $report,
        'numbers' => [
            0b1010,
            0o755,
            0xDEADBEEF,
            1_000_000,
            123.456e-7,
        ],
        'strings' => [
            'single quoted string with \\\' escape',
            "double quoted string with {$result['anonymous']}",
            <<<'NOWDOC'
Nowdoc content:
No interpolation here.
Symbols: []{}()<> !== === ?? ?-> :: ...
NOWDOC,
        ],
    ],
    JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR,
) . PHP_EOL;