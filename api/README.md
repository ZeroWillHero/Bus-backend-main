# SL-Bus API contract

`openapi.yaml` is the contract for every HTTP endpoint the backend serves. It is
embedded into the server binary (`api/api.go`) and published at:

| URL | What |
|---|---|
| `/docs` | Swagger UI (try requests, authorize with a bearer token) |
| `/openapi.yaml` | the raw spec, for code generators and Postman |

Production: <https://api.slbus.codescapelabs.com/docs>. Locally: <http://localhost:8080/docs>.

## Keeping it correct

The spec is maintained by hand. When you add or change a route, update
`openapi.yaml` in the same commit. `go test ./cmd/server -run TestOpenAPISpecMatchesRoutes`
(part of the normal test run and the pre-commit hook) fails when:

- a route is registered but not documented,
- the spec documents a route the server doesn't register, or
- the file is not a valid OpenAPI 3 document.

It does **not** check request/response shapes, so review those in the diff.

Things worth knowing:

- Most endpoints use the `{success, data, meta}` / `{success:false, error}` envelope. The
  **booking and ticket endpoints do not**: they return the bare object and a bare
  `{code, message}` error. The spec documents what the server really does (`BookingError`),
  so generated clients work today. If those handlers are later moved onto the envelope, update the spec too.
- Paginated lists return `meta.nextCursor` / `meta.hasMore`; pass the cursor back as `?cursor=`.
- The WebSocket route (`/v1/ws/trips/{tripID}/seats`) is listed for completeness. OpenAPI can't
  model WebSockets, so its frames are described by the `SeatStatusMessage` schema.

## Generating Dart types and API clients (Flutter)

Both Flutter apps already use `dio`, `freezed` and `json_serializable`, so the
[`swagger_parser`](https://pub.dev/packages/swagger_parser) package fits: it reads the spec and writes
freezed models plus Retrofit/Dio clients (`AuthApi`, `FleetApi`, `BookingsApi`, ...).

In the app (customer or operator):

```bash
# 1. dependencies
flutter pub add retrofit
flutter pub add -d swagger_parser retrofit_generator
# dio, freezed_annotation, json_annotation, freezed, json_serializable, build_runner are already there

# 2. config: copy the template
cp ../sl-bus-backend/api/flutter/swagger_parser.yaml .

# 3. generate (re-run whenever openapi.yaml changes)
dart run swagger_parser
dart run build_runner build --delete-conflicting-outputs
```

Use it:

```dart
final dio = Dio(BaseOptions(baseUrl: 'https://api.slbus.codescapelabs.com'));
final res = await FleetApi(dio).listBuses();
print(res.data.first.registrationNo);
```

Add the access token with a Dio interceptor (`Authorization: Bearer <token>`). Don't edit
the generated files; they are overwritten on each run. The generated `RealtimeApi` is not
usable (WebSockets); use `web_socket_channel` and decode frames with the generated
`SeatStatusMessage`.

Other generators work from the same file if you prefer: `openapi-generator` (`dart-dio`)
needs Java; `dart_mappable`-style output is available in `swagger_parser` via
`json_serializer: dart_mappable`.
