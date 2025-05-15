package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-redis/redis/v8"
)

// RedisRepository provides methods to store and retrieve patient requests in Redis.
type RedisRepository struct {
	client *redis.Client
	ctx    context.Context
}

// DocMsg represents a message sent to a doctor, stored in Redis.
type DocMsg struct {
	ChatID int64 `json:"chat_id"`
	MsgID  int   `json:"msg_id"`
}

// NewRedisRepository initializes a Redis client.
// addr is Redis server address (e.g. "localhost:6379"), password and db select the Redis database.
func NewRedisRepository(addr, password string, db int) *RedisRepository {
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	return &RedisRepository{client: client, ctx: ctx}
}

// AddDocMsg appends a DocMsg to the patient's list of sent messages.
func (r *RedisRepository) AddDocMsg(userID int64, dm DocMsg) error {
	key := fmt.Sprintf("patientReq:%d", userID)
	data, err := json.Marshal(dm)
	if err != nil {
		return err
	}
	return r.client.RPush(r.ctx, key, data).Err()
}

// GetDocMsgs retrieves all DocMsg entries for a given patient.
func (r *RedisRepository) GetDocMsgs(userID int64) ([]DocMsg, error) {
	key := fmt.Sprintf("patientReq:%d", userID)
	vals, err := r.client.LRange(r.ctx, key, 0, -1).Result()
	if err != nil {
		return nil, err
	}
	var result []DocMsg
	for _, v := range vals {
		var dm DocMsg
		if err := json.Unmarshal([]byte(v), &dm); err != nil {
			// пропускаем некорректные записи
			continue
		}
		result = append(result, dm)
	}
	return result, nil
}

// DeleteDocMsgs removes all stored DocMsg entries for a patient.
func (r *RedisRepository) DeleteDocMsgs(userID int64) error {
	key := fmt.Sprintf("patientReq:%d", userID)
	return r.client.Del(r.ctx, key).Err()
}
