package todo

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Store struct {
	path string
}

func NewStore(home string) Store {
	return Store{path: filepath.Join(home, ".todos.json")}
}

func (s Store) Load() ([]Task, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return []Task{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return []Task{}, nil
	}

	var tasks []Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s Store) Save(tasks []Task) error {
	data, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(s.path, data, 0o644)
}

func (s Store) Add(text string) (Task, error) {
	tasks, err := s.Load()
	if err != nil {
		return Task{}, err
	}

	nextID := 1
	for _, task := range tasks {
		if task.ID >= nextID {
			nextID = task.ID + 1
		}
	}

	newTask := Task{
		ID:        nextID,
		Text:      text,
		Done:      false,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	tasks = append(tasks, newTask)
	if err := s.Save(tasks); err != nil {
		return Task{}, err
	}

	return newTask, nil
}

func (s Store) MarkDone(id int) (Task, error) {
	tasks, err := s.Load()
	if err != nil {
		return Task{}, err
	}

	for i := range tasks {
		if tasks[i].ID == id {
			tasks[i].Done = true
			if err := s.Save(tasks); err != nil {
				return Task{}, err
			}
			return tasks[i], nil
		}
	}

	return Task{}, fmt.Errorf("task %d not found", id)
}

func (s Store) Delete(id int) error {
	tasks, err := s.Load()
	if err != nil {
		return err
	}

	filtered := make([]Task, 0, len(tasks))
	found := false
	for _, task := range tasks {
		if task.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, task)
	}
	if !found {
		return fmt.Errorf("task %d not found", id)
	}

	return s.Save(filtered)
}
