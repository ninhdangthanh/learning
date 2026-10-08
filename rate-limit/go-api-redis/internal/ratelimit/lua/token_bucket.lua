local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local refill_per_second = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local cost = tonumber(ARGV[4])

local state = redis.call('HMGET', key, 'tokens', 'updated_at')
local tokens = tonumber(state[1])
local updated_at = tonumber(state[2])

if tokens == nil or updated_at == nil then
  tokens = capacity
  updated_at = now
end

local elapsed = math.max(0, now - updated_at) / 1000
tokens = math.min(capacity, tokens + elapsed * refill_per_second)

local allowed = 0
local retry = 0

if tokens >= cost then
  tokens = tokens - cost
  allowed = 1
else
  retry = math.ceil(((cost - tokens) / refill_per_second) * 1000)
end

redis.call('HSET', key, 'tokens', tokens, 'updated_at', now)
redis.call('PEXPIRE', key, math.ceil((capacity / refill_per_second) * 1000) + 1000)

local reset = math.ceil(((capacity - tokens) / refill_per_second) * 1000)

return {allowed, math.floor(tokens), reset, retry}
