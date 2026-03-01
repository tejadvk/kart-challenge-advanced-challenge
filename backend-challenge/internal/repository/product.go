package repository

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/yourusername/kart-challenge/backend-challenge/internal/models"
)

// ProductRepo handles product data access
type ProductRepo struct {
	products []models.Product
	byID     map[string]*models.Product
}

// NewProductRepo loads products from the given JSON file path
func NewProductRepo(dataPath string) (*ProductRepo, error) {
	data, err := os.ReadFile(dataPath)
	if err != nil {
		return nil, err
	}

	var products []models.Product
	if err := json.Unmarshal(data, &products); err != nil {
		return nil, err
	}

	byID := make(map[string]*models.Product)
	for i := range products {
		byID[products[i].ID] = &products[i]
	}

	return &ProductRepo{
		products: products,
		byID:     byID,
	}, nil
}

// NewProductRepoFromModulePath loads products.json relative to the executable/working dir
func NewProductRepoFromModulePath() (*ProductRepo, error) {
	// Try paths relative to common project layout
	paths := []string{
		"data/products.json",
		"backend-challenge/data/products.json",
		"../data/products.json",
	}

	execPath, err := os.Executable()
	if err == nil {
		base := filepath.Dir(execPath)
		paths = append([]string{
			filepath.Join(base, "data/products.json"),
		}, paths...)
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return NewProductRepo(p)
		}
	}

	return NewProductRepo("data/products.json")
}

// GetAll returns all products
func (r *ProductRepo) GetAll() []models.Product {
	return r.products
}

// GetByID returns a product by ID, or nil if not found
func (r *ProductRepo) GetByID(id string) *models.Product {
	return r.byID[id]
}
