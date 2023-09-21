#if defined UP4W_TEST_WITH_MS_STORE_MOCK && defined _MSC_VER

#include "WinMockContext.hpp"

#include <Windows.h>
#include <processenv.h>
#include <winrt/Windows.Data.Json.h>
#include <winrt/Windows.Foundation.Collections.h>
#include <winrt/Windows.Foundation.h>
#include <winrt/Windows.Web.Http.h>
#include <winrt/base.h>

#include <algorithm>
#include <cassert>
#include <iterator>
#include <sstream>
#include <unordered_map>

#include "WinRTHelpers.hpp"

namespace StoreApi::impl {

using winrt::Windows::Data::Json::IJsonValue;
using winrt::Windows::Data::Json::JsonArray;
using winrt::Windows::Data::Json::JsonObject;
using UrlParams = std::unordered_multimap<winrt::hstring, winrt::hstring>;
using winrt::Windows::Foundation::Uri;

using winrt::Windows::Foundation::IAsyncOperation;

namespace {
// Handles the HTTP calls, returning a JsonObject containing the mock server
// response. Notice that the supplied path is relative.
IAsyncOperation<JsonObject> call(winrt::hstring relativePath,
                                 UrlParams const& params = {});

/// Creates a product from a JsonObject containing the relevant information.
WinMockContext::Product fromJson(JsonObject const& obj);

}  // namespace

std::vector<WinMockContext::Product> WinMockContext::GetProducts(
    std::span<const std::string> kinds,
    std::span<const std::string> ids) const {
  assert(!kinds.empty() && "kinds vector cannot be empty");
  assert(!ids.empty() && "ids vector cannot be empty");

  auto hKinds = to_hstrings(kinds);
  auto hIds = to_hstrings(ids);

  UrlParams parameters;

  std::ranges::transform(
      hKinds, std::inserter(parameters, parameters.end()),
      [](winrt::hstring k) { return std::make_pair(L"kinds", k); });
  std::ranges::transform(
      hIds, std::inserter(parameters, parameters.end()),
      [](winrt::hstring id) { return std::make_pair(L"ids", id); });

  auto productsJson = call(L"/products", parameters).get();

  JsonArray products = productsJson.GetNamedArray(L"products");

  std::vector<WinMockContext::Product> result;
  result.reserve(products.Size());
  for (const IJsonValue& product : products) {
    JsonObject p = product.GetObject();
    result.emplace_back(fromJson(p));
  }

  return result;
}

std::vector<std::string> WinMockContext::AllLocallyAuthenticatedUserHashes() {
  JsonObject usersList = call(L"/allauthenticatedusers").get();
  JsonArray users = usersList.GetNamedArray(L"users");

  std::vector<std::string> result;
  result.reserve(users.Size());
  for (const IJsonValue& user : users) {
    result.emplace_back(winrt::to_string(user.GetString()));
  }

  return result;
}

std::string WinMockContext::GenerateUserJwt(std::string token,
                                            std::string userId) const {
  assert(!token.empty() && "Azure AD token is required");
  JsonObject res{nullptr};

  UrlParams parameters{
      {L"serviceticket", winrt::to_hstring(token)},
  };
  if (!userId.empty()) {
    parameters.insert({L"publisheruserid", winrt::to_hstring(userId)});
  }

  res = call(L"generateuserjwt", parameters).get();

  return winrt::to_string(res.GetNamedString(L"jwt"));
}

namespace {
// Returns the mock server endpoint address and port by reading the environment
// variable UP4W_MS_STORE_MOCK_ENDPOINT or localhost:9 if the variable is unset.
winrt::hstring readStoreMockEndpoint() {
  constexpr std::size_t endpointSize = 20;
  wchar_t endpoint[endpointSize];
  if (0 == GetEnvironmentVariableW(L"UP4W_MS_STORE_MOCK_ENDPOINT", endpoint,
                                   endpointSize)) {
    return L"127.0.0.1:9";  // Discard protocol
  }
  return endpoint;
}

// Builds a complete URI with a URL encoded query if params are passed.
IAsyncOperation<Uri> buildUri(winrt::hstring& relativePath,
                              UrlParams const& params) {
  // Being tied t an environment variable means that it cannot change after
  // program's creation. Thus, there is no reason for recreating this value
  // every call.
  static winrt::hstring endpoint = L"http://" + readStoreMockEndpoint();

  if (!params.empty()) {
    winrt::Windows::Web::Http::HttpFormUrlEncodedContent p{
        {params.begin(), params.end()},
    };
    auto rawParams = co_await p.ReadAsStringAsync();
    // http://127.0.0.1:56567/relativePath?param=value...
    co_return Uri{endpoint, relativePath + L'?' + rawParams};
  }
  // http://127.0.0.1:56567/relativePath
  co_return Uri{endpoint, relativePath};
}

IAsyncOperation<JsonObject> call(winrt::hstring relativePath,
                                 UrlParams const& params) {
  // Initialize only once.
  static winrt::Windows::Web::Http::HttpClient httpClient{};

  Uri uri = co_await buildUri(relativePath, params);

  // We can rely on the fact that our mock will return small pieces of data
  // certainly under 1 KB.
  winrt::hstring contents = co_await httpClient.GetStringAsync(uri);
  co_return JsonObject::Parse(contents);
}

WinMockContext::Product fromJson(JsonObject const& obj) {
  std::chrono::system_clock::time_point tp{};
  std::stringstream ss{winrt::to_string(obj.GetNamedString(L"ExpirationDate"))};
  ss >> std::chrono::parse("%FT%T%Tz", tp);

  return WinMockContext::Product{
      winrt::to_string(obj.GetNamedString(L"StoreID")),
      winrt::to_string(obj.GetNamedString(L"Title")),
      winrt::to_string(obj.GetNamedString(L"Description")),
      winrt::to_string(obj.GetNamedString(L"ProductKind")),
      tp,
      obj.GetNamedBoolean(L"IsInUserCollection")};
}

}  // namespace
}  // namespace StoreApi::impl
#endif  // UP4W_TEST_WITH_MS_STORE_MOCK
