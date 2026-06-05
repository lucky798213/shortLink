wrk.method = "POST"
wrk.headers["Content-Type"] = "application/json"

function init(args)
  math.randomseed(os.time() * 1000 + math.random(1, 1000))
end

function randomString(length, charset)
  local res = ""
  for i = 1, length do
    local rand = math.random(#charset)
    res = res .. string.sub(charset, rand, rand)
  end
  return res
end

function request()
  local charset = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
  local randomStr = randomString(32, charset)
  local body = string.format('{"origin_url":"https://example.com/%s"}', randomStr)
  return wrk.format(nil, "/api/short-links", nil, body)
end
