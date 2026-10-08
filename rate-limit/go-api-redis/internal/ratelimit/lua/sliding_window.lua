local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local now = tonumber(ARGV[3])

local bucket = math.floor(now / window)
local elapsed = now - bucket * window
local overlap = (window - elapsed) / window
local reset = math.floor(window - elapsed)

local current_key = key .. ':' .. bucket
local previous_key = key .. ':' .. (bucket - 1)

local current = tonumber(redis.call('GET', current_key)) or 0
local previous = tonumber(redis.call('GET', previous_key)) or 0
local estimated = previous * overlap + current

if estimated + 1 > limit then
  return {0, 0, reset, reset}
end

current = redis.call('INCR', current_key)
if current == 1 then
  redis.call('PEXPIRE', current_key, window * 2)
end

local remaining = math.floor(limit - (previous * overlap + current))
if remaining < 0 then
  remaining = 0
end

return {1, remaining, reset, 0}
