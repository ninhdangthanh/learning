local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local now = tonumber(ARGV[3])

local bucket = math.floor(now / window)
local bucket_key = key .. ':' .. bucket
local reset = math.floor((bucket + 1) * window - now)

local count = redis.call('INCR', bucket_key)
if count == 1 then
  redis.call('PEXPIRE', bucket_key, window)
end

if count > limit then
  return {0, 0, reset, reset}
end

return {1, limit - count, reset, 0}
