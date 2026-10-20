# Fragment-reuse precedents in other ecosystems, Go SQL parsers/deparsers, and compile-time vs runtime expansion

State as of 2026-10-19. Repo metadata (stars, license, last push, latest release) was pulled from the GitHub REST API and the Go module proxy on 2026-10-19; module versions/dates come from `proxy.golang.org/<module>/@latest`. Anything not backed by a fetched page is marked UNVERIFIED.

---

## A1. MyBatis XML mappers: `<sql id>` + `<include refid>` (+ `<property>`)

### Takeaway
MyBatis is the canonical *load-time* text-substitution precedent: a `<sql>` fragment is a named block of SQL, `<include>` splices it at mapper-load time, and `<property>` children parametrize the fragment via `${}` string substitution (not bind variables). Includes can nest and even the `refid` itself can be computed from a property.

### Cited Findings
- `<sql>` "can be used to define a reusable fragment of SQL code that can be included in other statements." It "can be statically (during load phase) parametrized," and "Different property values can vary in include instances." Example: `<sql id="userColumns"> ${alias}.id,${alias}.username,${alias}.password </sql>` — [MyBatis sqlmap-xml](https://mybatis.org/mybatis-3/sqlmap-xml.html)
- Reference syntax: `<include refid="userColumns"><property name="alias" value="t1"/></include>`; the doc's select includes the same fragment twice with aliases `t1` and `t2` — [MyBatis sqlmap-xml](https://mybatis.org/mybatis-3/sqlmap-xml.html)
- Fragment "parameters" are only `<property>` values substituted into `${name}` placeholders; `${}` injects text as-is: "Here MyBatis won't modify or escape the string," with the warning "It's not safe to accept input from a user and supply it to a statement unmodified in this way." — [MyBatis sqlmap-xml](https://mybatis.org/mybatis-3/sqlmap-xml.html)
- Dynamic include target: "Property value can be also used in include refid attribute or property values inside include clause." Example: fragment `sometable` contains `${prefix}Table`; fragment `someinclude` contains `from <include refid="${include_target}"/>`; the select includes `someinclude` with properties `prefix=Some` and `include_target=sometable` (nested include) — [MyBatis sqlmap-xml](https://mybatis.org/mybatis-3/sqlmap-xml.html)

### Inferences
- Because expansion is pure text substitution at load time, there is no renumbering problem: MyBatis uses named `#{}` bind variables resolved after expansion, so fragment bind parameters merge for free. This is the same property sqlc's named `sqlc.arg()` / `@name` parameters would give a pre-expansion tool.
- Result-type inference is not a concern for MyBatis (it maps rows by name at runtime), so it offers no precedent for static typing after expansion.

### Gaps
- The doc page does not state whether `#{}` bind variables work inside `<sql>` fragments (its fragment examples use only `${}`); widely used in practice but UNVERIFIED from the official page.

---

## A2. HugSQL snippets (Clojure) and the Yesql / aiosql / PugSQL / anosql family

### Takeaway
HugSQL is the only tool in the "SQL-in-files with `-- :name` headers" family that has a real fragment mechanism: snippets (`-- :snip`) are functions that return an *sqlvec* (`["sql with ? placeholders" param1 param2 ...]`), and they are spliced into a query at **runtime** through `:snip:name` / `:snip*:names` parameters, with the snippet's positional parameters concatenated onto the caller's vector. Yesql, aiosql, PugSQL and anosql have no fragment feature at all. There is no `:frag` feature in HugSQL (nothing in its changelog or docs).

### Cited Findings
- Snippet definition uses `-- :snip` instead of `-- :name`: `-- :snip select-snip` / `select :i*:cols`, `-- :snip from-snip` / `from :i*:tables`, `-- :snip where-snip` / `where :snip*:cond` — [HugSQL snippets](https://www.hugsql.org/using-hugsql/composability/snippets)
- Snippets are consumed by a query via `:snip:` parameters: `-- :name snip-query :? :*` / `:snip:select` / `:snip:from` / `--~ (when (:where params) ":snip:where")`, called as `(snip-query db {:select (select-snip {:cols ["id","name"]}) :from (from-snip {:tables ["test"]}) :where (where-snip {:cond [(cond-snip {...}) ...]})})` — [HugSQL snippets](https://www.hugsql.org/using-hugsql/composability/snippets)
- "A snippet returns an sqlvec, which gives flexibility. You can supply snippets built by another library, such as HoneySQL" — [HugSQL snippets](https://www.hugsql.org/using-hugsql/composability/snippets)
- sqlvec format: "a vector with an SQL string in the first position containing any `?` placeholders, followed by any number of parameter values to be applied to the SQL in positional order", e.g. `["select * from characters where id = ?", 2]` — [HugSQL def-sqlvec-fns doc (repo)](https://github.com/layerware/hugsql/blob/master/docs/hugsql.org/docs/using-hugsql/def-sqlvec-fns.md)
- Docs: "Snippet Parameters are replaced at runtime with the provided snippet/sqlvec." and "Snippet List Parameters are replaced at runtime with the provided list of snippets/sqlvecs." — [snippet-parameters.md](https://github.com/layerware/hugsql/blob/master/docs/hugsql.org/docs/hugsql-in-detail/parameter-types/snippet-parameters.md), [snippet-list-parameters.md](https://github.com/layerware/hugsql/blob/master/docs/hugsql.org/docs/hugsql-in-detail/parameter-types/snippet-list-parameters.md)
- Merge semantics (source): `:snip` and `:snip*` are aliases of `:sqlvec`/`:sqlvec*` (`(defmethod apply-hugsql-param :snip ... (sqlvec-param ...))`); `sqlvec-param` returns the sqlvec as-is, and `sqlvec-param-list` reduces a list of sqlvecs by joining the SQL strings with a single space and `concat`-ing the parameter tails: `(apply vector (string/join " " [(first %1) (first %2)]) (concat (rest %1) (rest %2)))` — [hugsql parameters.clj](https://github.com/layerware/hugsql/blob/master/hugsql-core/src/hugsql/parameters.clj)
- `snip` is literally `(def snip "Alias for `sqlvec`" sqlvec)`; headers accept `:name`, `:name-`, `:snip`, `:snip-` (private) — [hugsql core.clj](https://github.com/layerware/hugsql/blob/master/hugsql-core/src/hugsql/core.clj)
- Clojure expressions (`--~` single-line, `/*~ ... ~*/` multi-line) let SQL be composed conditionally at runtime; "Expressions should return a `String` or `nil`" and may contain HugSQL parameter syntax such as `:i*:cols` — [HugSQL Clojure expressions](https://www.hugsql.org/using-hugsql/composability/clojure-expressions)
- HugSQL repo: 709 stars, Apache-2.0, last push 2024-12-02 — [GitHub API, layerware/hugsql](https://github.com/layerware/hugsql)
- HugSQL changelog mentions snippets and sqlvec but contains no "frag" entry — [HugSQL CHANGELOG](https://github.com/layerware/hugsql/blob/master/CHANGELOG.md)
- Yesql: `-- name:` with `!` / `<!` suffixes, `:name` params, `IN (:id)` lists; the README describes no fragment or composition feature ("Drop the same SQL files into other projects" is file-level reuse); status "Thawing. Assistance maintaining sought."; EPL-1.0; it points to HugSQL as the more active alternative — [Yesql README](https://github.com/krisajenkins/yesql); 1,497 stars, last push 2024-11-23 — [GitHub API](https://github.com/krisajenkins/yesql)
- aiosql: `-- name:` operators `^` (one), `$` (value), `!` (exec), `<!` (returning id), `*!` (many), `#` (script); named `:param` only; declared parameters checked ("raising errors when parameters are unused or undeclared"); no fragment/snippet/include mechanism on the defining-queries page — [aiosql Defining SQL queries](https://nackjicholson.github.io/aiosql/defining-sql-queries.html); 1,413 stars, release 15.0 on 2026-01-04 — [GitHub API](https://github.com/nackjicholson/aiosql)
- PugSQL: `-- :name find_user :one`, `:user_id` params, "inspired by the amazing HugSQL library"; no fragment feature on the site — [pugsql.org](https://pugsql.org/); README has no snippet/fragment/include mention — [PugSQL README](https://github.com/mcfunley/pugsql); 777 stars, v0.3.7 on 2025-12-04 — [GitHub API](https://github.com/mcfunley/pugsql)
- anosql: "This project is now deprecated in favor of aiosql" — [anosql README](https://github.com/honza/anosql); archived, last push 2020-09-09 — [GitHub API](https://github.com/honza/anosql)

### Inferences
- HugSQL's splice rule ("insert the SQL text; append the parameter tail in textual order") is exactly what a positional-placeholder renumbering pass must do; it works because `?` placeholders are implicitly ordered. The same rule with `$N` placeholders requires explicit renumbering of every `$N` in the fragment by the count of parameters already emitted before the splice point.
- The `-- :snip` / `-- :name` header symmetry is a natural syntax precedent for a sqlc fragment tool (e.g. a `-- name: X :fragment`-style header), and HugSQL's private `:snip-` shows the value of distinguishing fragments that should not become public functions.
- HugSQL snippets have no static typing of results; type inference after expansion is not addressed by any tool in this family.

### Gaps
- The public hugsql.org "sqlvec parameters" page returned HTTP 403 twice; merge semantics above come from source code instead of the rendered docs.

---

## A3. dbt Jinja macros and `ref()` / ephemeral CTE inlining

### Takeaway
dbt expands Jinja macros to plain SQL at **compile time** (`dbt compile`, output in `target/compiled/`), passing arguments as Jinja values; `ref()` resolves to a database relation name, and only *ephemeral* models are inlined into dependents as CTEs. dbt documents whitespace control and "readability over DRY" as the main pitfalls of text-level macros.

### Cited Findings
- Macro definition: `{% macro cents_to_dollars(column_name, scale=2) %} ({{ column_name }} / 100)::numeric(16, {{ scale }}) {% endmacro %}`; call: `{{ cents_to_dollars('amount') }} as amount_usd`; packaged macros are qualified: `{{ dbt_utils.dimensions(5) }}` — [dbt Jinja and macros](https://docs.getdbt.com/docs/build/jinja-macros)
- Compiled output is plain SQL: "Run `dbt compile` from the command line. Then open the compiled SQL file in the `target/compiled/{project name}/` directory." — [dbt Jinja and macros](https://docs.getdbt.com/docs/build/jinja-macros)
- Pitfalls: unquoted string args ("the Jinja parser will look for a variable named `amount`... it will compile to nothing"); whitespace control with `{%-`/`-%}` ("it's easy to fall down a rabbit hole when it comes to whitespace control!"); "Remember that using Jinja can make your models harder for other users to interpret... even if it means repeating some lines of SQL in a few places."; v1.10+ `validate_macro_args` flag warns about documented/actual argument mismatches — [dbt Jinja and macros](https://docs.getdbt.com/docs/build/jinja-macros)
- `ref()` "Returns a Relation for a model, seed, or snapshot", "Compiles to the full object name in the database", and "Creates dependencies between the referenced node and the current model" — [dbt ref](https://docs.getdbt.com/reference/dbt-jinja-functions/ref)
- Ephemeral materialization: "`ephemeral` models are not directly built into the database. Instead, dbt will interpolate the code from an ephemeral model into its dependent models using a common table expression (CTE)... dbt will always prefix the model identifier with `__dbt__cte__`." Cons: "You cannot select directly from this model", "Overuse of ephemeral materialization can also make queries harder to debug", "doesn't support model contracts" — [dbt materializations](https://docs.getdbt.com/docs/build/materializations)

### Inferences
- dbt's ephemeral model is the closest "build-time expansion into CTEs" precedent: the reused unit is a whole SELECT, spliced as a named CTE so the dependent query stays valid SQL. For a sqlc tool, expanding a fragment that is a full SELECT into a CTE (rather than inline text) keeps the expanded query parseable by sqlc with no renumbering of columns.
- dbt has no result-type inference; its warnings about whitespace and debuggability apply directly to any text-template approach (the exact approach the sqlc maintainer floated, see A8).

### Gaps
- None material.

---

## A4. Doma 2-way SQL, JDBI SqlObject templating (`@Define`, StringTemplate), jOOQ plain SQL templating

### Takeaway
Doma shows how to keep template directives inside SQL comments so files stay executable as-is, but has no cross-file include. JDBI's templating (`<name>` placeholders, `@Define`, StringTemplate `.sql.stg` group files) is runtime text rendering with an explicit "common attack vector" warning. jOOQ's plain SQL templating (`{0}` QueryPart placeholders, `?` binds) is the most rigorous runtime composition precedent: it has a mini-parser that ignores placeholders inside comments/strings/quoted names and renders nested parts' bind values in order.

### Cited Findings
**Doma**
- Directives are SQL comments followed by test data ignored at runtime: bind variable `/* employeeId */99` → `?`; literal `/*^ code */'test'`; embedded variable `/*# orderBy */` inserts text directly; `/*%if*/`, `/*%for*/`, `/*%expand "alias"*/*` (SELECT list from entity), `/*%populate*/` (UPDATE SET list), `/*%! */` parser comment — [Doma SQL templates](https://docs.domaframework.org/en/latest/sql/)
- No include/shared-fragment mechanism is described; only RDBMS-specific file variants (`-postgres.sql`) and `SqlTemplate` from the doma-template module — [Doma SQL templates](https://docs.domaframework.org/en/latest/sql/)
- Pitfalls: literal directive "do[es] not escape parameters for SQL injection" and rejects `'`; embedded directive rejects `'`, `;`, `--`, `/*`; `/*%if*/.../*%end*/` pairs must sit in the same clause and statement level; `/*` followed by an identifier char or `%`,`#`,`@`,`"`,`'` is a directive, so ordinary comments should use `/** ... */` — [Doma SQL templates](https://docs.domaframework.org/en/latest/sql/)

**JDBI**
- "Templating (using a TemplateEngine) allows you to alter a query's content with general String manipulations... Unlike argument binding, the rendering of attributes performed by TemplateEngines is not SQL-aware... TemplateEngines can easily produce horribly mangled or subtly defective queries if you don't use them carefully. Query templating is a common attack vector! Always prefer binding parameters to static SQL over dynamic SQL when possible." Example: `handle.createQuery("SELECT * FROM <TABLE> WHERE name = :n").define("TABLE", "Person").bind("n", "MyName")` — [Jdbi 3 Developer Guide §15.4 Query Templating](https://jdbi.org/)
- Default engine: "Unless explicitly configured, Jdbi uses a simple render engine that replaces `<…>` placeholders"; alternatives `jdbi3-stringtemplate4`, `jdbi3-commons-text`, `jdbi3-freemarker`; template/parse caching via `jdbi3-caffeine-cache` / `jdbi3-noop-cache` — [Jdbi 3 Developer Guide §2.3.1](https://jdbi.org/)
- `@Define`: "Defines a named attribute as the argument passed to the annotated parameter. Attributes are stored on the StatementContext, and may be used by statement customizers such as the template engine." — [Jdbi @Define javadoc](https://jdbi.org/apidocs/org/jdbi/v3/sqlobject/customizer/Define.html)
- StringTemplate group file: `com/foo/AccountDao.sql.stg`: `group AccountDao; selectAll(sort,sortBy) ::= << SELECT id, name FROM account ORDER BY <if(sort)> <sortBy>, <endif> id >>`; in SQL objects `@SqlQuery @UseStringTemplateSqlLocator List<Account> selectAll(@Define boolean sort, @Define String sortBy);` locates the template by method name (override via `@SqlQuery("listSorted")`) — [Jdbi 3 Developer Guide §18.15.2](https://jdbi.org/)
- "Since StringTemplate by default uses the < character to mark ST expressions, you might need to escape some SQL: `<if(datePredicate)> <dateColumn> \\< :dateFilter <endif>`" — [Jdbi 3 Developer Guide §18.15.1](https://jdbi.org/)
- StringTemplate itself supports calling one template from another: `<template(argument-list)>` ("expr is evaluated in the context of the surrounding template not of the invoked template"), indirect `<(expr)(args)>`, and `import "group file.stg"` — [StringTemplate 4 cheat sheet](https://github.com/antlr/stringtemplate4/blob/master/doc/cheatsheet.md)
- Current JDBI release: 3.55.0 on 2026-09-26 (listed in the guide's release table) — [Jdbi 3 Developer Guide](https://jdbi.org/)

**jOOQ**
- A plain SQL template has "The SQL string fragment containing `{number}` style placeholders" plus QueryPart arguments; "The SQL string may reference the arguments by 0-based indexing. Each argument may be referenced several times." e.g. `condition("{0} && {1}", array1, array2)`; `DSL.list(a, b, c)` collapses a list into one argument: `condition("my_column IN ({0})", list(a, b, c))` — [jOOQ plain SQL templating](https://www.jooq.org/doc/latest/manual/sql-building/plain-sql-templating/)
- The templating engine runs a mini parser recognizing string literals (`'abc'`, `$$abc$$`), quoted names (`"name"`, `` `name` ``, `[name]`), comments (`/* */`, `--`), JDBC escapes, and bind placeholders (`?` or `:identifier`); placeholders inside those constructs are skipped — [jOOQ plain SQL templating](https://www.jooq.org/doc/latest/manual/sql-building/plain-sql-templating/)
- Two API forms: `method(String, Object...)` bind values for `?` placeholders, and QueryParts "injected at the position of their respective placeholders"; "the two API usages can also be mixed, although this is not recommended and the exact behaviour is unspecified"; `{identifier}` denotes a keyword — [jOOQ plain SQL QueryParts](https://www.jooq.org/doc/latest/manual/sql-building/queryparts/plain-sql-queryparts/)
- "The number of ? must match the number of variables."; "jOOQ can't prevent SQL injection or transform your SQL string in any way."; `@PlainSQL` checker annotation can forbid plain SQL — [jOOQ plain SQL API](https://www.jooq.org/doc/latest/manual/sql-building/plain-sql/plain-sql-api/)
- Bind indexing: "When the query is rendered, each bind value will render a question mark", "When the query binds its variables, each bind value will generate the appropriate bind value index"; `getBindValues()` returns values in render order — [jOOQ indexed parameters](https://www.jooq.org/doc/latest/manual/sql-building/bind-values/indexed-parameters/)

### Inferences
- jOOQ's "skip placeholders inside comments, strings and quoted identifiers" rule is the minimum lexical awareness a fragment expander needs even if it does not build a full AST; a tokenizer (e.g. libpg_query's scanner, see B1) is enough for that.
- JDBI's and Doma's injection warnings arise because their expansion happens at runtime with user-influenced attributes. A generate-time expander that only splices developer-authored fragment text and leaves all values as bind parameters has no such exposure.
- Doma's comment-directive design is the precedent for keeping files valid SQL; sqlc's own `-- name:` header comments follow the same idea, so fragment markers as comments (`-- fragment:` / `sqlc.fragment(...)` inside a comment) would keep raw `.sql` files loadable by psql.

### Gaps
- JDBI docs do not show a shared-fragment example across templates; cross-template reuse in `.sql.stg` relies on StringTemplate's own include syntax (cheat sheet cited), not on a Jdbi feature.

---

## A5. PgTyped, Kysely, Drizzle `sql` tag, Prisma TypedSQL (TypeScript)

### Takeaway
PgTyped and Prisma TypedSQL are *generate-time, live-database* typers with one standalone query per unit and **no** fragment composition. Kysely and Drizzle do runtime composition with tagged templates: nested `sql` fragments are inlined, their values become parameters collected in traversal order and numbered per dialect at compile time (`$N` Postgres, `?` MySQL/SQLite, `@N` MSSQL), and result types are developer-asserted generics (`sql<T>`) that are never runtime-checked.

### Cited Findings
**PgTyped**
- Queries in `.sql` files: `/* @name getAllComments */ SELECT * FROM book_comments WHERE id = :commentId;`; params `:name`, non-null `:name!`; expansions declared with `@param name -> (...)`, `-> (name, age)`, `-> ((name, age)...)`; output nullability overrides via `AS "name?"` / `AS "name!"`; no fragments/includes mentioned — [PgTyped SQL files](https://pgtyped.dev/docs/sql-file)
- Types come from the running database: "by using your running Postgres database as the source of type information"; "`@pgtyped/runtime` is the only required runtime dependency"; 3.3k stars, MIT — [adelsz/pgtyped README](https://github.com/adelsz/pgtyped)

**Kysely**
- Substitutions "are never interpolated to the SQL string"; substitutions can be values, other `sql` expressions, queries, or `ExpressionBuilder.ref` results, and "the generated SQL numbers all the parameters together ($1, $2, ...)"; helpers `sql.ref`, `sql.table`, `sql.id`, `sql.lit`, `sql.raw` ("simply glued to the parent string as-is"), `sql.join`, `sql.val`; unchecked input "WILL lead to SQL injection vulnerabilities"; `sql<T>` types the result — [Kysely Sql interface](https://kysely-org.github.io/kysely-apidoc/interfaces/Sql.html)
- Placeholder numbering per dialect in source: default compiler `getCurrentParameterPlaceholder()` returns `'$' + this.numParameters` and `appendValue()` pushes the parameter then appends the placeholder — [default-query-compiler.ts](https://github.com/kysely-org/kysely/blob/master/src/query-compiler/default-query-compiler.ts); MySQL returns `'?'` — [mysql-query-compiler.ts](https://github.com/kysely-org/kysely/blob/master/src/dialect/mysql/mysql-query-compiler.ts); SQLite returns `'?'` — [sqlite-query-compiler.ts](https://github.com/kysely-org/kysely/blob/master/src/dialect/sqlite/sqlite-query-compiler.ts); MSSQL returns `` `@${this.numParameters}` `` — [mssql-query-compiler.ts](https://github.com/kysely-org/kysely/blob/master/src/dialect/mssql/mssql-query-compiler.ts)

**Drizzle**
- "Any dynamic parameters such as `${id}` will be mapped to the $1 placeholder"; "sql chunks, which are arrays of SQL parts that will be concatenated into the query and params"; `sql.fromList`, `sql.join(chunks, separator)`, `sql.append`, `sql.empty`; `sql.raw` inserts text "without any additional processing or escaping"; `sql<T>` "does not perform any runtime mapping", `.mapWith(Number)` for runtime conversion, `.as('alias')`; dialect must be chosen to render (`new PgDialect().sqlToQuery(...)`) — [Drizzle magic sql operator](https://orm.drizzle.team/docs/sql)

**Prisma TypedSQL**
- One `.sql` file per query in `prisma/sql`; `prisma generate --sql`; params `$1`/`?`/`:name` by engine with `-- @param {Int} $1:minAge` annotations, nullable via `$1:name?`; "TypedSQL requires an active database connection to function properly"; inference for PostgreSQL and MySQL 8.0+, manual annotation for older MySQL and all SQLite; no fragment/composition described; dynamic column selection unsupported (falls back to `$queryRawUnsafe`) — [Prisma TypedSQL](https://www.prisma.io/docs/orm/prisma-client/using-raw-sql/typedsql)

### Inferences
- Kysely/Drizzle demonstrate the general "number placeholders in emission order at build time" algorithm; the same algorithm applied once at generate time is what a sqlc pre-expander would do for `$N` dialects, while `?` dialects need no renumbering at all.
- Every static-typing tool surveyed (PgTyped, Prisma, sqlx, Cornucopia) types a *whole* query via the database; none types fragments independently. A sqlc expander therefore cannot type a fragment in isolation and should expand first, then let sqlc type the expanded whole.

### Gaps
- The Kysely docs page on raw SQL is only an intro; parameter-merging behavior is cited from source rather than prose docs.

---

## A6. Rust: sqlx `query!`, Cornucopia, Diesel

### Takeaway
sqlx `query!` is compile-time checked against a live database (or offline `.sqlx` data) but the SQL "must be a string literal, or concatenation of string literals using `+`" so fragments cannot be composed at the macro level; runtime composition goes through `QueryBuilder`, which is explicitly unchecked. Cornucopia/Clorinde generate typed Rust from `.sql` files via a live Postgres with no fragment feature. Diesel composes typed query *fragments* in Rust (boxed expressions, `QueryFragment::walk_ast`), achieving type-preserving composition at the cost of Rust-level typing rather than SQL text.

### Cited Findings
- sqlx `query!`: "The `DATABASE_URL` environment variable must be set at build-time to point to a database server with the schema that the query string will be checked against." Offline: "`.sqlx` must exist at the workspace root." "The query must be a string literal, or concatenation of string literals using `+`" — otherwise it "cannot be introspected (and thus cannot be dynamic or the result of another macro)"; `query_file!` for out-of-line SQL; Postgres `$N`, MySQL/SQLite `?`; overrides `"id!"`, `"col?"`, `"col: T"`; "Postgres only infers nullability for columns that come directly from tables"; mistakes surface as `UnexpectedNull` at runtime — [sqlx query! macro](https://docs.rs/sqlx/latest/sqlx/macro.query.html)
- sqlx `QueryBuilder` (0.9.0): `push` "does _not_ perform sanitization"; `push_bind` emits "`?` or `$N` for Postgres"; "Note: Query is not Checked"; bind limits "Postgres and MySQL at 65535, SQLite at 32766 (or 999 before 3.32.0), and MSSQL at 2100" and "Exceeding these limits may panic" — [sqlx QueryBuilder](https://docs.rs/sqlx/latest/sqlx/struct.QueryBuilder.html)
- Cornucopia: queries in `.sql` files annotated `--! insert_author` with `:param_name` params; prepared against an actual database; no fragments mentioned; "Cornucopia 1.0 merged the Clorinde fork back into the original project" — [cornucopia README](https://github.com/cornucopia-rs/cornucopia); 1,163 stars, Apache-2.0 (API; README says Apache-2.0 OR MIT), latest release `cornucopia-v1.0.1` on 2026-08-14 — [GitHub API](https://github.com/cornucopia-rs/cornucopia); Clorinde archived 2026-05-21 after `clorinde-v2.0.0` — [GitHub API](https://github.com/halcyonnouveau/clorinde)
- Diesel composition: functions return query pieces typed via `diesel::dsl` helper types, `Box<BoxableExpression<...>>`, or `#[diesel::dsl::auto_type]`; boxing needs table, backend and SQL type; "You cannot box an aggregate expression in Diesel"; "a boxed expression can only be used with _exactly_ the from clause given"; "you should always prefer functions that return queries" — [Diesel composing applications](https://diesel.rs/guides/composing-applications.html)
- `QueryFragment`: "An untyped fragment of SQL" that may be "a complete SQL command" or "a subsection"; `walk_ast` "is where the actual behavior of an AST node is implemented. This method will contain the behavior required for all possible AST passes." — [Diesel QueryFragment](https://docs.rs/diesel/latest/diesel/query_builder/trait.QueryFragment.html)

### Inferences
- sqlx's literal-only rule is a deliberate choice that trades composability for introspectability; a Go pre-expander sidesteps it by producing literal SQL *before* the typing tool runs.
- Diesel's "boxed expression is tied to its FROM clause" limitation is the same type-conflict problem raised in sqlc issue #2244 (a WHERE fragment reused across tables with different column types); any sqlc fragment design must expect per-call-site types.

### Gaps
- Diesel's `is_safe_to_cache_prepared` / prepared-statement-cache interaction was not on the fetched page: UNVERIFIED.

---

## A7. SQLDelight (Kotlin) and Elixir Ecto `fragment/1`

### Takeaway
SQLDelight infers types at compile time from `.sq` schema files (no live DB), supports named/positional args, collection args, and *grouped statements* (`name { stmt; stmt; }`) that share parameters, and allows `CREATE VIEW` in `.sq` files — but documents no snippet/fragment feature. Ecto's `fragment("...", args)` is a runtime-composed raw-SQL escape hatch whose first argument must be a compile-time literal (enforced in the query builder to prevent injection), reusable only by wrapping in Elixir macros, with no type casting inside fragments.

### Cited Findings
- SQLDelight "generates typesafe Kotlin APIs from your SQL statements. It verifies your schema, statements, and migrations at compile-time."; dialects SQLite, MySQL, PostgreSQL, HSQL/H2 (experimental) plus third-party CockroachDB/DB2/Oracle — [SQLDelight index](https://sqldelight.github.io/sqldelight/latest/)
- Labeled statements `selectAll: SELECT * FROM hockeyPlayer;` generate functions per `.sq` file (`PlayerQueries`); plugin version 2.4.0 — [SQLDelight JVM SQLite](https://sqldelight.github.io/sqldelight/latest/jvm_sqlite/)
- Arguments: "Named parameters or indexed parameters can be used." (`:name` used twice → one Kotlin parameter); `WHERE full_name IN ?` accepts a collection; "SQLDelight will infer the correct type and nullability of runtime parameters, including custom column types." — [SQLDelight arguments](https://sqldelight.github.io/sqldelight/latest/jvm_sqlite/query_arguments/)
- Grouping: "You can group multiple SQL statements together to be executed at once inside a transaction:" `upsert { UPDATE myTable SET column1 = :column1, column2 = :column2 WHERE id = :id; INSERT OR IGNORE INTO myTable (id, column1, column2) VALUES (:id, :column1, :column2); }` — [SQLDelight grouping statements](https://sqldelight.github.io/sqldelight/latest/jvm_sqlite/grouping_statements/)
- Grouping shipped in 1.4.0 ("[Compiler] Syntax for grouping multiple SQL statements together in a .sq file") after issue #809 — [SQLDelight CHANGELOG](https://github.com/cashapp/sqldelight/blob/master/CHANGELOG.md), [issue #809](https://github.com/cashapp/sqldelight/issues/809)
- Views live in `.sq` files (issue title: "Data classes for a table's queries aren't generated if one of its VIEWs is moved to another table's .sq file") — [SQLDelight issue #2313](https://github.com/cashapp/sqldelight/issues/2313)
- Ecto `fragment`: "Every occurrence of the `?` character will be interpreted as a place for parameters, which must be given as additional arguments to `fragment`"; literal `?` escaped as `\\?`; reusable via macro: `defmacro coalesce(left, right) do quote do fragment("coalesce(?, ?)", unquote(left), unquote(right)) end end`; "Ecto is unable to do any type casting when fragments are used" → use `type/2` — [Ecto.Query.API](https://ecto.hexdocs.pm/Ecto.Query.API.html)
- Enforcement in source: "to prevent SQL injection attacks, fragment(...) does not allow strings to be interpolated as the first argument via the `^` operator" — [ecto/lib/ecto/query/builder.ex](https://github.com/elixir-ecto/ecto/blob/master/lib/ecto/query/builder.ex)

### Inferences
- SQLDelight's grouped statements are the precedent for "one name, several statements sharing one parameter set" — relevant if a sqlc fragment tool wants to emit list/count/sum variants of one query from one source (the pain point reported in sqlc #2244, see A8).
- Ecto's literal-only rule mirrors sqlx: static tools accept composition only when the composed text is known at compile time, which a Go pre-expander satisfies.

### Gaps
- No SQLDelight issue requesting cross-query fragments was found by web search (GitHub search API was rate-limited); treat "no fragment feature in SQLDelight" as supported by docs absence only.

---

## A8. Build-time fragment expansion with preserved static typing: closest analogues (incl. sqlc's own proposals)

### Takeaway
No surveyed tool expands fragments at build time *and* statically types the result; the nearest things are (a) sqlc's two open proposals (#2244 "Query Fragments", #2491 "expand VIEWs at generation"), where the maintainer suggested a Jinja-style macro preprocessor and commenters report using Go templates as a pre-step, (b) dbt ephemeral models (CTE inlining, no typing), (c) MyBatis load-time includes (no typing), and (d) SQLDelight/sqlc views (typed, but must exist in the schema).

### Cited Findings
- sqlc #2244 (opened 2023-04-30, renamed "Query Fragments" 2023-05-02, still open, labels postgresql/golang/enhancement): proposes a `:fragment` annotation on a named block and `sqlc.fragment(Name)` to embed it; generated params would embed a `WorkspaceCheckFragmentParams` struct inside the query's params; the author "expected that the syntax might not parse as valid SQL"; a colleague raised the type-conflict problem when a fragment is used on tables whose argument types differ (per-table param types suggested) — [sqlc issue #2244](https://github.com/sqlc-dev/sqlc/issues/2244)
- Maintainer kyleconroy (2023-09-26) "did not add new `sqlc.xxx` syntax" and instead "proposed a Jinja2-style macro preprocessor, with an example macro `workspace_check` invoked as `{{ workspace_check() }}`, and asked 'Would that solve your issue?'"; no accepted design, no linked PR — [sqlc issue #2244](https://github.com/sqlc-dev/sqlc/issues/2244)
- Workarounds in the thread: views (but `sqlc.embed` cannot be used inside a view), Go templates as preprocessing (drawback: "losing valid SQL in the source", which Emyrk called "a huge upside to SQLc"), `CASE WHEN` for varying WHERE clauses, Postgres functions (rejected: need migrations); wilianto (2024-11-18) reports the maintenance burden of list/count/sum variants — [sqlc issue #2244](https://github.com/sqlc-dev/sqlc/issues/2244)
- sqlc #2491 (opened 2023-07-21 by Emyrk, open, no maintainer comment): "handle `VIEW`s as reusable queries and never committing them to postgres"; proposed syntax `-- name: book_with_author :view` / `CREATE VIEW book_with_author AS ...` then `SELECT * FROM sqlc.view(book_with_author) WHERE id = $1 LIMIT 1;`; rationale quotes PostgreSQL docs "The view is not physically materialized. Instead, the query is run every time the view is referenced in a query."; related to #2244 and #112 — [sqlc issue #2491](https://github.com/sqlc-dev/sqlc/issues/2491)
- sqlc named params: `sqlc.arg(name)` / `@name` (shorthand "not supported in MySQL") set Params field names; `sqlc.narg(name)` forces nullable — [sqlc named parameters](https://docs.sqlc.dev/en/latest/howto/named_parameters.html)
- sqlc `sqlc.embed(table)` in the select list yields a row struct with one model field per table (`type ScoreAndTestsRow struct { Student Student; TestScore TestScore }`) — [sqlc embedding](https://docs.sqlc.dev/en/latest/howto/embedding.html)
- xo (renamed dbtpl in May 2025) query mode: "In query mode, `dbtpl` parses your query to generate code from Go templates. It finds related tables in your database to ensure type safety."; embedded params like `a.id = %%authorID int%%` with `--interpolate`; no fragment feature; 3.9k stars, MIT — [xo/xo README](https://github.com/xo/xo)
- dbt ephemeral CTE inlining and MyBatis load-time includes: see A3 and A1.

### Inferences
- The sqlc maintainer's own suggestion (a macro preprocessor producing plain SQL that sqlc then types) is precisely a "pre-expansion" architecture; the community objection is loss of valid-SQL source files and the lack of a standard tool. A tool that keeps fragment definitions and references inside SQL comments (Doma-style) satisfies both.
- #2491's `sqlc.view()` idea and dbt's ephemeral CTE are the same mechanism: expand a named SELECT as a CTE at generate time. It composes with sqlc typing because the expanded query is ordinary SQL.
- The type-conflict concern (#2244) implies fragment parameters should be typed per expansion site (sqlc already infers param types per query), i.e. do not try to generate a shared `FragmentParams` struct across queries unless types are proven identical.

### Gaps
- GitHub search API rate limits prevented searching sqlc issues for "snippet"/"reuse"; #112 ("Query templates") was referenced by #2491 but not fetched. Other researchers (sqlc-official/internals) may cover it.

---

## B1. PostgreSQL parsers/deparsers in Go: pg_query_go, go-pgquery (WASM), sqlc-dev/oliphant, auxten/postgresql-parser, cockroachdb-parser; what sqlc uses

### Takeaway
pg_query_go v6 (cgo, PostgreSQL 17 grammar, BSD-3) remains the reference parser with Parse/Deparse/Normalize/Fingerprint; wasilibs/go-pgquery is a pure-Go WASM drop-in that is ~4–5x slower; and as of sqlc's `main` branch (Oct 2026) sqlc has replaced both with **sqlc-dev/oliphant**, a hand-written pure-Go port claiming byte-equal Parse/Deparse/Fingerprint parity with pg_query_go v6 (sqlc v1.31.1, April 2026, still used pg_query_go v6.2.2 + go-pgquery). CockroachDB-derived parsers (auxten, cockroachdb-parser) are pure Go but follow CockroachDB's dialect; cockroachdb-parser was archived on 2026-10-06. None of these preserve comments in the parse tree; libpg_query exposes comments only through its scanner.

### Cited Findings
**pganalyze/pg_query_go**
- "Go library to parse and normalize SQL queries using the PostgreSQL query parser"; uses "the actual PostgreSQL server source"; API examples: `Parse` (Go protobuf structs), `ParseToJSON`, `Deparse`, `ParsePlPgSqlToJSON` (experimental); "Due to compiling parts of PostgreSQL, the first time you build against this library it will take a bit longer. Expect up to 3 minutes."; "it is currently not recommended to pass unsanitized input to the deparser, as it may lead to crashes."; BSD-3-Clause plus LICENSE.POSTGRESQL; README JSON sample shows `"version":170004` — [pg_query_go README](https://github.com/pganalyze/pg_query_go)
- 864 stars, BSD-3-Clause, last push 2026-10-01 — [GitHub API](https://github.com/pganalyze/pg_query_go); latest module `github.com/pganalyze/pg_query_go/v6` v6.2.5 (2026-09-30); `/v5` latest v5.1.0 (2024-01-10); `/v7` returns nothing on the module proxy as of 2026-10-19 — [proxy.golang.org](https://proxy.golang.org/github.com/pganalyze/pg_query_go/v6/@latest)
- Version/PG parity from CHANGELOG: 6.2.5 (2026-09-30) "Upgrade to libpg_query 17-6.2.5" with a Normalize heap OOB security fix (GHSA-6ggm-xmc9-8ffg) and deparser "strict checking for unexpected pointer values"; 6.2.2 (2026-01-27) libpg_query 17-6.2.2 adds `IsUtilityStmt` and `Summary`; 6.1.0 (2025-02-24) Postgres 17.4; 6.0.0 (2024-11-26) "Updates to the Postgres 17 parser"; 5.0.0 (2023-12-23) "Updates to the Postgres 16 parser", "Allow importing parse tree proto with cgo disabled"; 4.2.0 (2023-02-08) "Update to PostgreSQL 15.1"; 2.x releases 2021–2022 — [pg_query_go CHANGELOG](https://github.com/pganalyze/pg_query_go/blob/main/CHANGELOG.md)
- libpg_query (C core): 1,488 stars, latest release 18.1.0 on 2026-09-30 — [GitHub API](https://github.com/pganalyze/libpg_query); the 18-latest README shows `"version": 180006` and notes Postgres 18 changed `queryid` handling of schema names/aliases (fingerprint option `PG_QUERY_FINGERPRINT_RANGEVAR_PG17_COMPAT`) — [libpg_query README (18-latest)](https://github.com/pganalyze/libpg_query/blob/18-latest/README.md)
- Scanner exposes comments as tokens: input `SELECT update AS left /* comment */ FROM between` yields `"/* comment */" = [ 22, 35, C_COMMENT, NO_KEYWORD ]` with byte offsets — [libpg_query README](https://github.com/pganalyze/libpg_query/blob/17-latest/README.md)
- pganalyze: "Each release, the deparser is tested for completeness against the full set of Postgres regression tests"; Go binding updated alongside Ruby and Rust (post dated 2024-01-11) — [pganalyze blog, pg_query for Postgres 16](https://pganalyze.com/blog/pg-query-postgres-16)

**wasilibs/go-pgquery**
- "drop-in replacement for pg_query_go that, by default, compiles libpg_query's C code to WebAssembly and runs it with the pure Go runtime, wazero"; parse trees reuse pg_query_go's types so both modules are needed; `pgquery_cgo` build tag switches to cgo ("exactly the same code as `pg_query_go`"); cannot be used with TinyGo; MIT — [go-pgquery README](https://github.com/wasilibs/go-pgquery)
- Benchmarks (one run, GitHub runners): ParseSelect1 24.574µs (wasm) vs 6.558µs (cgo, −73%); ParseSelect2 82.56µs vs 21.68µs; ParseCreateTable 186.70µs vs 51.30µs; "the WebAssembly version performs about 4-5x slower than cgo" — [go-pgquery README](https://github.com/wasilibs/go-pgquery/blob/main/README.md)
- go.mod: requires `github.com/pganalyze/pg_query_go/v6 v6.2.5` and `github.com/tetratelabs/wazero v1.12.0`, go 1.25.0 — [go-pgquery go.mod](https://github.com/wasilibs/go-pgquery/blob/main/go.mod); module pseudo-version v0.0.0-20261013080023 (2026-10-13); 60 stars, last push 2026-10-19 — [GitHub API](https://github.com/wasilibs/go-pgquery)

**sqlc-dev/oliphant (new, pure Go)**
- "A hand-written, pure Go (no cgo, no wasm) drop-in replacement for pganalyze/pg_query_go: same exported functions, same generated protobuf types, same JSON output, same error type, same fingerprints. Consumers change one import path" (`-import pg_query "github.com/pganalyze/pg_query_go/v6"` → `+import pg_query "github.com/sqlc-dev/oliphant"`); "Pinned to libpg_query `18.0.0` (PostgreSQL 18.4, patched) — the exact build pg_query_go v6.2.2 ships"; status table: Lexer `Scan`/`SplitWithScanner`/`HashXXH3_64` byte-identical; Parse/ParseToJSON full grammar; `Normalize`, `Fingerprint`; "9. Deparse ✅ `Deparse`, byte-equal to the oracle across the corpus"; `ParsePlPgSqlToJSON`; "the corpus — 2,067 golden files / 308,561 cases across eleven suites — passes with an empty todo list"; milestones 12–13 (hardening, sqlc integration) marked "—" — [oliphant README](https://github.com/sqlc-dev/oliphant)
- go.mod depends only on go-cmp and protobuf (go 1.24) — [oliphant go.mod](https://github.com/sqlc-dev/oliphant/blob/main/go.mod); created 2026-08-15, 0 stars, license reported as NOASSERTION by the API — [GitHub API](https://github.com/sqlc-dev/oliphant)
- NOTE (conflict): oliphant's README says pg_query_go v6.2.2 ships libpg_query "18.0.0", but pg_query_go's CHANGELOG says 6.2.2 = "libpg_query 17-6.2.2" and the README sample shows version 170004. Treat the exact PG version pinned by oliphant as UNVERIFIED.

**sqlc usage**
- sqlc v1.31.1 (released 2026-04-22) go.mod: `github.com/antlr4-go/antlr/v4 v4.13.1`, `github.com/pganalyze/pg_query_go/v6 v6.2.2`, `github.com/pingcap/tidb/pkg/parser v0.0.0-20260418072757-ce92298d1124`, `github.com/sqlc-dev/doubleclick v1.0.0`, `github.com/tetratelabs/wazero v1.11.0`, `github.com/wasilibs/go-pgquery v0.0.0-20250409022910-10ac41983c07` — [sqlc go.mod @ v1.31.1](https://github.com/sqlc-dev/sqlc/blob/v1.31.1/go.mod)
- sqlc `main` go.mod (fetched 2026-10-19): `go 1.26.0`; `github.com/sqlc-dev/darkwing v0.1.0`, `doubleclick v1.0.0`, `marino v0.3.1`, `meyer v0.1.2`, `oliphant v0.2.0`, `teesql v1.1.0`, `zetajones v0.2.0`, `wazero v1.12.0`, `pgx/v5 v5.11.0`, `antlr4-go/antlr/v4 v4.13.1 // indirect`; no pg_query_go, go-pgquery or tidb parser — [sqlc go.mod @ main](https://github.com/sqlc-dev/sqlc/blob/main/go.mod)
- `internal/engine/postgresql/parse.go` on main imports `nodes "github.com/sqlc-dev/oliphant"` and `"github.com/sqlc-dev/oliphant/parser"` — [sqlc postgresql/parse.go](https://github.com/sqlc-dev/sqlc/blob/main/internal/engine/postgresql/parse.go)
- Engine directories on main: clickhouse, dolphin (MySQL), duckdb, googlesql, mssql, postgresql, sqlite — [sqlc internal/engine](https://github.com/sqlc-dev/sqlc/tree/main/internal/engine); sqlc: 18,394 stars, MIT, latest release v1.31.1 (2026-04-22), last push 2026-10-19 — [GitHub API](https://github.com/sqlc-dev/sqlc)
- Sibling sqlc-dev parsers: darkwing = "hand-written, zero-dependency Go port of DuckDB v2.0's PEG-based SQL parser" (vendors DuckDB's `.gram`/`.list` files, packrat matcher) — [darkwing README](https://github.com/sqlc-dev/darkwing); doubleclick = ClickHouse parser (62 stars, MIT) — [doubleclick README](https://github.com/sqlc-dev/doubleclick); teesql = T-SQL parser with SqlScriptDOM-compatible JSON AST — [teesql README](https://github.com/sqlc-dev/teesql); zetajones = pure-Go GoogleSQL parser passing "all ~4,700 cases from google/googlesql release 2026.01.1" byte-for-byte — [zetajones README](https://github.com/sqlc-dev/zetajones)

**CockroachDB-derived**
- auxten/postgresql-parser: "Pure golang implementation" split from CockroachDB v20.1.11's `pkg/sql/parser` and `pkg/sql/lex`; `parser.Parse(sql)` + `walk.AstWalker`; "Almost full support" of PostgreSQL "in CockroachDB style"; users Atlas and ByteBase; Apache-2.0 — [auxten README](https://github.com/auxten/postgresql-parser); 314 stars, v1.0.1 (2022-08-03), last push 2026-05-30 — [GitHub API](https://github.com/auxten/postgresql-parser)
- cockroachdb/cockroachdb-parser: "a snapshot of the parser package and its dependencies from the CockroachDB repo", `parser.ParseOne("SELECT 1")`, versions follow CockroachDB with a `v0.` prefix; "was archived by the owner on Oct 6, 2026" — [cockroachdb-parser README](https://github.com/cockroachdb/cockroachdb-parser); 44 stars, Apache-2.0, latest release v0.25.2.5.0 (2025-09-17); proxy latest v0.25.2 (2025-06-05) — [GitHub API](https://github.com/cockroachdb/cockroachdb-parser)

### Inferences
- Comment preservation: PostgreSQL's parse tree has no comment nodes (comments are only visible via the scanner's `C_COMMENT` tokens with byte offsets), so any Parse→Deparse round trip of a whole file drops `-- name:` headers and `sqlc.*` annotations embedded in comments. An expansion tool should therefore edit the *text* (using parser node locations and scanner token offsets to find splice points) rather than deparse whole files.
- For a generate-time tool, parse cost (tens of µs per statement even in WASM) is irrelevant; the important choices are cgo-free builds (go-pgquery or oliphant) and AST/location fidelity (pg_query_go-compatible protobuf nodes carry `location` fields, which is what sqlc itself relies on).
- Since sqlc itself now consumes oliphant on main, a fragment tool targeting sqlc should use the same pg_query_go-compatible node types (either module) so expansions and sqlc see identical trees.

### Gaps
- go-pgquery's embedded WASM blob size (binary-size impact) could not be located (candidate paths 404): UNVERIFIED.
- pg_query_go's README does not document `Scan`/`Normalize`/`Fingerprint` in prose; their presence is inferred from oliphant's "same exported functions" claim and the libpg_query README.
- Whether sqlc v1.31.1 selects pg_query_go (cgo) vs go-pgquery (wasm) by build tag is not confirmed here (likely covered by the sqlc-internals researcher).

---

## B2. MySQL parsers in Go: Vitess sqlparser, dolthub/vitess, TiDB parser (Restore API), sqlc-dev/marino, xwb1989/sqlparser, blastrain/vitess-sqlparser, go-mysql-server

### Takeaway
Two live MySQL parser lineages exist: Vitess (`vitess.io/vitess/go/vt/sqlparser`, yacc, keeps leading comments as `ParsedComments`, `String()` serializer) with Dolt's fork adding DDL/stored procedures/triggers, and the TiDB goyacc parser with an explicit `Restore(ctx *format.RestoreCtx)` round-trip API controlled by `RestoreFlags`. sqlc used the TiDB parser through v1.31.1 and on main uses **marino**, its own hand-written recursive-descent hard fork of pingcap/parser. xwb1989/sqlparser and blastrain/vitess-sqlparser are unmaintained since 2022.

### Cited Findings
- Vitess sqlparser on pkg.go.dev: module v0.24.5 published 2026-10-13; API includes `String(node)`, `CanonicalString(node)`, `Append(buf, node)`, `Walk`, `Clone`, `ParseAndBind`, `FetchBindVar`; types `Comments`, `ParsedComments`, `CommentDirectives`; Apache-2.0; 391 known importers — [pkg.go.dev vitess sqlparser](https://pkg.go.dev/vitess.io/vitess/go/vt/sqlparser)
- Parser API in source: `New(opts Options)`, `NewTestParser()`, `(*Parser).Parse`, `Parse2` (returns BindVars), `ParseMultiple`, `ParseStrictDDL`, `ParseExpr`, `SplitStatementToPieces`, `IsStatementIncomplete` — [vitess parser.go](https://github.com/vitessio/vitess/blob/main/go/vt/sqlparser/parser.go); statement structs carry `Comments *ParsedComments` fields and a `GetParsedComments()` accessor — [vitess ast.go](https://github.com/vitessio/vitess/blob/main/go/vt/sqlparser/ast.go)
- vitessio/vitess: 21,387 stars, Apache-2.0, latest release v24.0.5 (2026-10-13), last push 2026-10-19 — [GitHub API](https://github.com/vitessio/vitess)
- dolthub/vitess: adds "DDL statements, stored procedures, and triggers", prunes ~90% of upstream, "has diverged substantially"; Apache-2.0 — [dolthub/vitess README](https://github.com/dolthub/vitess); module `github.com/dolthub/vitess`, go 1.26.2 — [dolthub/vitess go.mod](https://github.com/dolthub/vitess/blob/main/go.mod); 28 stars, last push 2026-10-14 — [GitHub API](https://github.com/dolthub/vitess); consumed by go-mysql-server as `github.com/dolthub/vitess v0.0.0-20261014211039-3e903c0f05d3` — [go-mysql-server go.mod](https://github.com/dolthub/go-mysql-server/blob/main/go.mod); go-mysql-server: 2,660 stars, Apache-2.0, v0.20.0 (2025-05-13), last push 2026-10-19 — [GitHub API](https://github.com/dolthub/go-mysql-server)
- TiDB parser: "a Golang parser that is fully compatible with MySQL syntax", "generated by goyacc"; users listed include pingcap/tidb, XiaoMi/soar, bytebase/bytebase, kyleconroy/sqlc, block/spirit; Apache-2.0 — [TiDB parser README](https://github.com/pingcap/tidb/blob/master/pkg/parser/README.md); install via commit hash (`go get github.com/pingcap/tidb/pkg/parser@069631e`) and a `parser_driver` import is required — [TiDB parser quickstart](https://github.com/pingcap/tidb/blob/master/pkg/parser/docs/quickstart.md)
- Restore API: `ast.Node` has `Restore(ctx *format.RestoreCtx) error`, `Text()`, `OriginalText()`, `SetText`, `SetOriginTextPosition(offset int)` — [TiDB ast.go](https://github.com/pingcap/tidb/blob/master/pkg/parser/ast/ast.go); `RestoreFlags` include `RestoreStringSingleQuotes`, `RestoreKeyWordUppercase`, `RestoreNameBackQuotes` (together `DefaultRestoreFlags`), `RestoreTiDBSpecialComment`, `RestoreSkipRedundantParentheses` ("default SQL restore keeps user-written parentheses"), `NewRestoreCtx(flags, writer)`; module pseudo-version v0.0.0-20261019143132 (2026-10-19) — [pkg.go.dev parser/format](https://pkg.go.dev/github.com/pingcap/tidb/pkg/parser/format); pingcap/tidb 40,654 stars — [GitHub API](https://github.com/pingcap/tidb)
- sqlc-dev/marino: "a hard fork of pingcap/parser"; "the grammar is hand-written recursive descent — adding a new syntax is a few lines of ordinary Go"; "a single-pass hand-written parser with a streaming lexer and no parser-generator runtime"; Apache-2.0 — [marino README](https://github.com/sqlc-dev/marino); created 2026-04-26 — [GitHub API](https://github.com/sqlc-dev/marino); sqlc main `internal/engine/dolphin/parse.go` imports `github.com/sqlc-dev/marino/parser` — [sqlc dolphin/parse.go](https://github.com/sqlc-dev/sqlc/blob/main/internal/engine/dolphin/parse.go)
- xwb1989/sqlparser: "The backbone of this repo is extracted from vitessio/vitess", Apache-2.0 — [xwb1989 README](https://github.com/xwb1989/sqlparser); 1,584 stars, last push 2022-09-27, module pseudo-version 2018-06-06 — [GitHub API](https://github.com/xwb1989/sqlparser)
- blastrain/vitess-sqlparser: "simply SQL Parser for Go ( powered by vitess and TiDB )" (Vitess for SQL, TiDB for DDL); notes xwb1989 "can't parse some queries, such as OFFSET or bulk inserts" — [blastrain README](https://github.com/blastrain/vitess-sqlparser); 495 stars, last push 2022-11-14, pseudo-version 2020-10-30 — [GitHub API](https://github.com/blastrain/vitess-sqlparser)

### Inferences
- TiDB's `Restore` normalizes case/quoting by flag and re-emits only TiDB special comments; Vitess keeps only leading statement comments. Neither preserves arbitrary interior comments, so the same text-editing strategy as for PostgreSQL applies to MySQL.
- For a sqlc-aligned tool, marino (sqlc's fork) is the parser whose AST sqlc will consume going forward; its API stability is unknown given its 2026 creation date.

### Gaps
- Marino's exported API and whether it retains TiDB's `Restore` were not inspected.
- The Go module proxy returns a stale `v2.1.1+incompatible` (2017) for `github.com/dolthub/vitess` `@latest`; consumers use pseudo-versions, so "latest version" for this fork is only meaningful as the commit go-mysql-server pins.

---

## B3. SQLite parsers in Go: sqlc's ANTLR parser → sqlc-dev/meyer, rqlite/sql, antlr4-go runtime

### Takeaway
sqlc's SQLite engine used an ANTLR-generated parser (antlr4-go v4.13.1, a direct dependency in v1.31.1) and on main uses **meyer**, a zero-dependency hand-written recursive-descent SQLite parser built "to replace the ANTLR-generated SQLite parser in sqlc". rqlite/sql (MIT, Ben Johnson's parser) is the main independent pure-Go SQLite parser and has `String()` serializers on its AST nodes (83 in `ast.go`, including views).

### Cited Findings
- meyer: "A pure Go parser for the SQLite SQL dialect. Zero dependencies, hand-written recursive descent — no parser generators... meyer is being built to replace the ANTLR-generated SQLite parser in sqlc" — [meyer README](https://github.com/sqlc-dev/meyer); MIT, created 2026-07-30 — [GitHub API](https://github.com/sqlc-dev/meyer); sqlc main `internal/engine/sqlite/parse.go` imports `github.com/sqlc-dev/meyer/ast`, `/parser`, `/token` — [sqlc sqlite/parse.go](https://github.com/sqlc-dev/sqlc/blob/main/internal/engine/sqlite/parse.go)
- antlr4-go/antlr: "A release write-only repo for ANTLR4 Go target"; latest v4.13.1 (2024-05-15); BSD-3-Clause; 151 stars; last push 2024-06-28 — [GitHub API](https://github.com/antlr4-go/antlr), [proxy.golang.org](https://proxy.golang.org/github.com/antlr4-go/antlr/v4/@latest); sqlc v1.31.1 lists it as a direct dependency, main as `// indirect` — [sqlc go.mod v1.31.1](https://github.com/sqlc-dev/sqlc/blob/v1.31.1/go.mod), [sqlc go.mod main](https://github.com/sqlc-dev/sqlc/blob/main/go.mod)
- rqlite/sql: "Pure Go SQL parser based on the SQLite SQL definition"; "goal is to support the full SQLite syntax"; originally by Ben Johnson; MIT — [rqlite/sql README](https://github.com/rqlite/sql); 180 stars, last push 2026-10-16, module pseudo-version 2026-10-16 — [GitHub API](https://github.com/rqlite/sql); `ast.go` defines 83 `String() string` methods, e.g. `func (s *CreateViewStatement) String() string`, plus `Clone()` and statement types from `ExplainStatement` to `CreateIndexStatement` — [rqlite/sql ast.go](https://github.com/rqlite/sql/blob/master/ast.go)

### Inferences
- With sqlc moving to meyer, a fragment tool for SQLite should either reuse meyer's AST (to match sqlc exactly) or stay text-based with a tokenizer; rqlite/sql is a reasonable independent choice if a `String()` round trip is wanted, but its fidelity for comments is undocumented.

### Gaps
- Comment handling and round-trip fidelity of rqlite/sql and meyer were not verified.

---

## B4. Generic / multi-dialect Go parsers: krasun/gosqlparser, marianogappa/sqlparser, sqlglot ports, xo/dbtpl

### Takeaway
The "simple" generic Go parsers (krasun, marianogappa) cover a toy subset and are not viable for real queries. There is no official Go binding for sqlglot; ridi-oss/sqlglot-go is a stdlib-only Go port of sqlglot's parser/AST/generator for Base, MySQL and Postgres dialects, tested only for same-dialect round trips.

### Cited Findings
- krasun/gosqlparser: CREATE TABLE, DROP TABLE, SELECT (WHERE, LIMIT), INSERT, UPDATE, DELETE; `{0}`/`{p}` placeholders; MIT — [gosqlparser README](https://github.com/krasun/gosqlparser); 80 stars, v1.0.5 (2021-12-30), last push 2025-01-15 — [GitHub API](https://github.com/krasun/gosqlparser)
- marianogappa/sqlparser: "Simple SQL parser meant for querying CSV files"; SELECT/UPDATE/DELETE/INSERT subset, WHERE mandatory for UPDATE/DELETE; MIT — [marianogappa README](https://github.com/marianogappa/sqlparser); 423 stars, last push 2021-08-02 — [GitHub API](https://github.com/marianogappa/sqlparser)
- ridi-oss/sqlglot-go: "Go port of the parse → AST → generate core of tobymao/sqlglot (pinned to v30.17.0), plus the `qualify` and `scope` optimizer passes"; dialects Base, MySQL, Postgres; "Same-dialect round-trip only... 1847/1847 corpus cases passing"; cross-dialect transpilation "not verified"; MIT; v0.37.4 published 2026-10-12 (page notes a newer version exists) — [pkg.go.dev sqlglot-go](https://pkg.go.dev/github.com/ridi-oss/sqlglot-go)
- No official Go binding for sqlglot was found; shynome/polyglot-go is a purego binding to a different C library (polyglot_sql) — [WebSearch result](https://pkg.go.dev/github.com/shynome/polyglot-go)
- xo/dbtpl: see A8 (schema/query introspection tool, not a parser).

### Inferences
- For AST-aware expansion the realistic choices are the engine-specific parsers in B1–B3; a generic parser would misparse dialect features that sqlc accepts.

### Gaps
- sqlglot-go's latest version was not retrieved (pkg.go.dev flagged v0.37.4 as not latest).

---

## B-summary table (retrieved 2026-10-19)

| Library | Dialect | Latest version (date) | Stars | License | Deparse / round trip | cgo | Last push | Notes |
|---|---|---|---|---|---|---|---|---|
| pganalyze/pg_query_go | PostgreSQL 17 grammar (libpg_query 17-6.2.5) | v6.2.5 (2026-09-30) | 864 | BSD-3-Clause | `Deparse` (regression-tested; "not recommended" for unsanitized trees); comments not in tree | yes (first build "up to 3 minutes") | 2026-10-01 | sqlc ≤ v1.31.1 |
| wasilibs/go-pgquery | same (wraps pg_query_go v6.2.5 via wazero) | pseudo 2026-10-13 | 60 | MIT | same API (deparse test present) | no (WASM); `pgquery_cgo` tag | 2026-10-19 | ~4–5x slower than cgo; no TinyGo; sqlc ≤ v1.31.1 |
| sqlc-dev/oliphant | PostgreSQL (pg_query_go v6 drop-in; pinned PG version UNVERIFIED) | v0.2.0 (sqlc main) | 0 | NOASSERTION (API) | `Deparse` "byte-equal to the oracle" | no | 2026-08-26 | sqlc main |
| auxten/postgresql-parser | CockroachDB v20.1.11 (PostgreSQL-style) | v1.0.1 (2022-08-03) | 314 | Apache-2.0 | format example linked; not documented | no | 2026-05-30 | users: Atlas, Bytebase |
| cockroachdb/cockroachdb-parser | CockroachDB | v0.25.2.5.0 (2025-09-17) | 44 | Apache-2.0 | not documented | no | archived 2026-10-06 | |
| vitess.io/vitess sqlparser | MySQL (+Vitess) | v0.24.5 (2026-10-13) | 21,387 | Apache-2.0 | `String`, `CanonicalString`; leading `ParsedComments` kept | no | 2026-10-19 | |
| dolthub/vitess | MySQL + DDL/procs/triggers | pseudo 2026-10-14 | 28 | Apache-2.0 | as Vitess | no | 2026-10-14 | used by go-mysql-server v0.20.0 |
| pingcap/tidb/pkg/parser | MySQL | pseudo 2026-10-19 | 40,654 (tidb) | Apache-2.0 | `Restore` with `RestoreFlags`; `OriginalText` | no | 2026-10-19 | sqlc ≤ v1.31.1; needs parser_driver |
| sqlc-dev/marino | MySQL (hard fork of pingcap/parser, recursive descent) | v0.3.1 (sqlc main) | 0 | Apache-2.0 | UNVERIFIED | no | 2026-08-28 | sqlc main |
| xwb1989/sqlparser | MySQL (old Vitess extract) | pseudo 2018-06-06 | 1,584 | Apache-2.0 | `String` | no | 2022-09-27 | unmaintained |
| blastrain/vitess-sqlparser | MySQL (Vitess + TiDB DDL) | pseudo 2020-10-30 | 495 | Apache-2.0 | — | no | 2022-11-14 | unmaintained |
| rqlite/sql | SQLite | pseudo 2026-10-16 | 180 | MIT | `String()` on all nodes (83) | no | 2026-10-16 | |
| sqlc-dev/meyer | SQLite (recursive descent) | v0.1.2 (sqlc main) | 2 | MIT | UNVERIFIED | no | 2026-08-25 | replaces ANTLR parser in sqlc |
| antlr4-go/antlr/v4 | runtime only | v4.13.1 (2024-05-15) | 151 | BSD-3-Clause | n/a | no | 2024-06-28 | sqlc v1.31.1 SQLite |
| ridi-oss/sqlglot-go | Base/MySQL/Postgres | v0.37.4 (2026-10-12, not latest) | UNVERIFIED | MIT | generator; same-dialect round trip only | no | UNVERIFIED | |
| krasun/gosqlparser | toy subset | v1.0.5 (2021-12-30) | 80 | MIT | — | no | 2025-01-15 | |
| marianogappa/sqlparser | toy subset (CSV) | pseudo 2021-08-02 | 423 | MIT | — | no | 2021-08-02 | |

Sources: GitHub API and proxy.golang.org as cited in B1–B4.

---

## C1. Splicing fragments that contain parameters; keeping `-- name:` and `sqlc.*` macros; line attribution

### Takeaway
Named parameters (`sqlc.arg()`/`@name`, HugSQL `:name`, MyBatis `#{}`) make fragment splicing a pure text operation because the typer assigns positions after expansion; positional placeholders require renumbering in emission order (`$N` by offset, `?` by order only), which every runtime composer (HugSQL, Kysely, Drizzle, jOOQ) implements as "append the fragment's parameters in textual order". Because no Go parser keeps comments in its tree, a tool must edit text using node/token offsets to keep sqlc's comment-based annotations, and must map expanded-file positions back to source positions for error messages.

### Cited Findings
- sqlc rewrites named parameters itself: `sqlc.arg(name)` and `@name` only influence Params field names; `@` shorthand is "not supported in MySQL"; `sqlc.narg` forces nullability — [sqlc named parameters](https://docs.sqlc.dev/en/latest/howto/named_parameters.html)
- sqlc's `internal/source` package exposes `LineNumber(source string, head int) (int, int)`, `Pluck(source, location, length)`, `Mutate(raw string, a []Edit) (string, error)`, `StripComments`, `CleanedComments` — i.e. sqlc computes line/column from byte offsets into the file it was given and applies edit lists to raw query text — [sqlc internal/source/code.go](https://github.com/sqlc-dev/sqlc/blob/main/internal/source/code.go)
- Positional merge rule (HugSQL): snippet lists are reduced by joining SQL with `" "` and concatenating parameter tails — [hugsql parameters.clj](https://github.com/layerware/hugsql/blob/master/hugsql-core/src/hugsql/parameters.clj)
- Emission-order numbering (Kysely): `appendValue` → `addParameter` then placeholder `'$' + numParameters` (Postgres), `'?'` (MySQL, SQLite), `@N` (MSSQL) — [default-query-compiler.ts](https://github.com/kysely-org/kysely/blob/master/src/query-compiler/default-query-compiler.ts) and dialect compilers cited in A5
- Drizzle: "Any dynamic parameters such as `${id}` will be mapped to the $1 placeholder", chunks "concatenated into the query and params" — [Drizzle sql](https://orm.drizzle.team/docs/sql)
- jOOQ skips placeholders inside comments, string literals, quoted names and JDBC escapes when splicing — [jOOQ plain SQL templating](https://www.jooq.org/doc/latest/manual/sql-building/plain-sql-templating/)
- PostgreSQL comments are available only from the scanner as `C_COMMENT` tokens with start/end offsets — [libpg_query README](https://github.com/pganalyze/libpg_query/blob/17-latest/README.md); Vitess keeps `Comments *ParsedComments` on statements — [vitess ast.go](https://github.com/vitessio/vitess/blob/main/go/vt/sqlparser/ast.go); TiDB `Restore` has a `RestoreTiDBSpecialComment` flag and nodes keep `OriginalText()` — [pkg.go.dev parser/format](https://pkg.go.dev/github.com/pingcap/tidb/pkg/parser/format), [TiDB ast.go](https://github.com/pingcap/tidb/blob/master/pkg/parser/ast/ast.go)
- Prisma's SQLite placeholders accept `$1`, `?` or `:name`; PostgreSQL `$1`; MySQL `?` — [Prisma TypedSQL](https://www.prisma.io/docs/orm/prisma-client/using-raw-sql/typedsql); sqlx: Postgres `$N`, MySQL/SQLite `?` — [sqlx query!](https://docs.rs/sqlx/latest/sqlx/macro.query.html)
- sqlc #2244's proposed fragment uses `sqlc.arg` inside the fragment and expects embedded fragment params structs; the type-conflict problem across tables was raised in-thread — [sqlc issue #2244](https://github.com/sqlc-dev/sqlc/issues/2244)

### Inferences
- Recommended rule set for a pre-expander: (1) require fragments to use named params (`sqlc.arg`/`sqlc.narg`/`@name` where the engine allows) so no renumbering is needed and sqlc dedupes repeated names; (2) if positional `$N` are allowed inside fragments, renumber by `N + (count of distinct positional params emitted before the splice point)`, treating `?` as order-only; (3) never touch text inside string literals, quoted identifiers or comments (jOOQ rule), which a scanner pass gives for free.
- Keeping annotations: splice at parser-provided byte locations into the *original text* and write a new `.sql` file; `-- name:` headers and `sqlc.embed/slice/narg` calls then survive untouched because they are never deparsed.
- Line attribution: because sqlc computes line numbers from offsets in the file it reads, errors will point at the generated file unless the tool (a) emits one generated line per source line where possible (fragments spliced inline on the same line keep line numbers stable; multi-line fragments shift subsequent lines) and (b) keeps a source map (generated line → source file:line) and rewrites sqlc's `file:line:col` messages when run as a wrapper. Emitting `-- source: path:line` marker comments inside generated files is a cheap alternative sqlc will ignore.

### Gaps
- How sqlc reports errors for queries inside multi-query files (exact `file:line:col` format) was not fetched here; the sqlc-internals researcher likely covers it.

---

## C2. Compile-time (go:generate-style) expansion vs runtime string assembly: cost per call, prepared-statement caches, binary size, benchmarks

### Takeaway
Pre-expansion makes every query a Go string constant, which costs nothing at runtime (constant expressions are evaluated at compile time; a constant string is static data) and gives stable SQL text, which is exactly what pgx's default statement cache keys on. Runtime assembly costs allocations per call (0 allocs for a single constant-folded `+`, 1–4 allocs for `strings.Builder`/`strings.Join`/`fmt.Sprintf` on tiny inputs, tens to hundreds of allocations and 2.6–84 µs per query for query builders in a 2015 benchmark) and risks defeating statement caches if the text varies.

### Cited Findings
**Go language / stdlib**
- "Constant expressions may contain only constant operands and are evaluated at compile time. Untyped boolean, numeric, and string constants may be used as operands wherever it is legal to use an operand of boolean, numeric, or string type" and "Strings can be concatenated using the + operator... String addition creates a new string by concatenating the operands." — [Go spec](https://go.dev/ref/spec)
- "When you expect to execute the same SQL repeatedly, you can use an `sql.Stmt` to prepare the SQL statement in advance"; "This can help your code run a bit faster by avoiding the overhead of re-creating the statement each time"; placeholders vary by driver ("the pq driver for Postgres requires a placeholder like `$1` instead of `?`") — [Go docs: prepared statements](https://go.dev/doc/database/prepared-statements)
- `sql.Stmt`: "If a Stmt is prepared on a DB, it will remain usable for the lifetime of the DB. When the Stmt needs to execute on a new underlying connection, it will prepare itself on the new connection automatically." — [database/sql sql.go](https://github.com/golang/go/blob/master/src/database/sql/sql.go)

**pgx**
- `QueryExecModeCacheStatement`: "Automatically prepare and cache statements. This uses the extended protocol. Queries are executed in a single round trip after the statement is cached. This is the default. If the database schema is modified or the search_path is changed after a statement is cached then the first execution of a previously cached query may fail."; `QueryExecModeCacheDescribe` caches descriptions; `QueryExecModeDescribeExec` "Queries require two round trips"; `QueryExecModeExec` assumes types from Go args; `QueryExecModeSimpleProtocol` "uses client side parameter interpolation" and "should only be used if connecting to a proxy server, connection pool server, or non-PostgreSQL server" — [pgx conn.go](https://github.com/jackc/pgx/blob/master/conn.go)
- Defaults: `statement_cache_capacity` "Default: 512.", `description_cache_capacity` "Default: 512.", `default_query_exec_mode` "Default: "cache_statement""; "By default pgx uses the extended protocol and automatically prepares and caches prepared statements." — [pkg.go.dev pgx/v5](https://pkg.go.dev/github.com/jackc/pgx/v5)
- The cache is keyed by the SQL text: `sd := c.statementCache.Get(sql)` / `c.descriptionCache.Get(sql)` in the exec-mode switch and in `getStatementDescription` — [pgx conn.go](https://github.com/jackc/pgx/blob/master/conn.go); server-side names are derived from the text: `StatementName(sql)` returns `"stmtcache_" + hex(sha256(sql)[0:24])`, "stable for sql across multiple connections and program executions" — [pgx stmtcache.go](https://github.com/jackc/pgx/blob/master/internal/stmtcache/stmtcache.go)
- pgx stdlib adapter "uses standard PostgreSQL positional parameters in queries. e.g. $1, $2. It does not support named parameters." — [pkg.go.dev pgx/v5/stdlib](https://pkg.go.dev/github.com/jackc/pgx/v5/stdlib); `Conn.PrepareContext`, `ExecContext`, `QueryContext` are implemented over the pgx connection — [pgx stdlib/sql.go](https://github.com/jackc/pgx/blob/master/stdlib/sql.go)

**Benchmarks (string building)**
- Small fixed concatenation (Feb 13, 2025, hardware unstated): `+` 27.43 ns/op, 7 B/op, 0 allocs; `strings.Builder` 93.52 ns/op, 3 allocs; `fmt.Sprintf` 128.20 ns/op, 4 allocs; `strings.Join` 70.22 ns/op, 1 alloc. Slice of words: `+` 363.5 ns / 6 allocs, Builder 189.8 ns / 4 allocs, Sprintf 472.1 ns / 9 allocs, Join 264.4 ns / 2 allocs — [dev.to: fmt.Sprintf looks simple...](https://dev.to/yudaph/fmtsprintf-looks-simple-but-will-burn-a-hole-in-your-pocket-h4h)
- Naive `+` in loops scales badly: 10 pieces 1192 ns / 11 allocs; 100 pieces 34,117 ns / 101 allocs; 1,000 pieces 2,641,900 ns / 1,004 allocs; `bytes.Buffer` 1354 ns / 8 allocs at 10 and 56,510 ns / 14 allocs at 1,000; conclusion: naive appending "is just fine" for very few short strings, `bytes.Buffer` for heavy use (Go version/machine unstated) — [hermanschaaf/go-string-concat-benchmarks](https://github.com/hermanschaaf/go-string-concat-benchmarks)
- Go toolchain example: switching `text/template/parse` String methods to `strings.Builder` moved BenchmarkVariableString from 296 to 115 ns/op and 8 to 3 allocs — [go.googlesource.com commit 4f13a9c](https://go.googlesource.com/go/+/4f13a9c5b1bfc9ec2213d9ee7d9df49661b119dd)

**Benchmarks (SQL query builders, 2015-era, "2.6 GHz i5 Macbook Pro", `go test -bench=. -benchmem`)**
- dbr SelectSimple 2,610 ns/op, 864 B/op, 14 allocs; SelectComplex 11,585 ns, 3,323 B, 53 allocs — squirrel SelectSimple 14,934 ns, 2,737 B, 52 allocs; SelectComplex 63,096 ns, 12,742 B, 283 allocs; SelectMoreComplex 83,842 ns, 17,153 B, 386 allocs; Insert 14,517 ns, 3,356 B, 75 allocs — sqrl SelectSimple 3,555 ns, 952 B, 15 allocs — goqu SelectSimple 15,180 ns, 3,282 B, 46 allocs; SelectComplex 50,628 ns, 11,414 B, 215 allocs — [elgris/golang-sql-builder-benchmark](https://github.com/elgris/golang-sql-builder-benchmark)
- No published benchmark comparing a *static string constant* against builder output was found; the comparison must be derived (a constant costs 0 ns and 0 allocs by construction).

**Generator-side cost (not application-side)**
- WASM parse costs ~25–190 µs per statement vs ~6.5–51 µs with cgo (see B1) — [go-pgquery README](https://github.com/wasilibs/go-pgquery/blob/main/README.md)

### Inferences
- Zero runtime cost is achievable because the expanded SQL becomes a `const` string in generated Go (the Go spec's compile-time evaluation of constant expressions): no allocation, no concatenation, no builder, and a stable byte sequence. sqlc already emits queries this way, so a pre-expander inherits that property for free.
- Stable text matters beyond CPU: pgx's cache and statement names are keyed on the exact SQL bytes, so runtime-assembled fragments with any whitespace or ordering difference create separate prepared statements (bounded by the 512-entry LRU) and separate server-side plans; pre-expanded constants yield exactly one cache entry per query per connection. database/sql `Stmt` objects likewise map 1:1 to pre-expanded constants and re-prepare automatically per connection.
- Binary size: pre-expansion adds only the bytes of the expanded SQL strings to the application binary (duplicated fragment text per use site), typically kilobytes; parser libraries (cgo libpg_query, the go-pgquery WASM blob, or oliphant's Go code) are linked only into the generator, not into applications, because expansion happens at `go generate` time. The WASM blob's exact size is UNVERIFIED.
- Runtime assembly also moves dialect placeholder logic (`$N` vs `?` vs `@N`) into the application; pre-expansion lets sqlc handle it once.

### Gaps
- No current (2024–2026) Go query-builder benchmark with allocation counts was found; the elgris numbers are from 2015 hardware and old library versions.
- Diesel's prepared-statement caching of composed fragments (`is_safe_to_cache_prepared`) and pgx behaviour under PgBouncer transaction pooling were not fetched.
