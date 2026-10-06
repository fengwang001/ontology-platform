package matching

func validateSupplier(supplier Supplier, at int64) error {
	if supplier.ID == "" || at < 0 {
		return errorf(ErrInvalidArgument, "invalid supplier")
	}
	return nil
}

func validatePurchaseOrder(order PurchaseOrder, at int64) error {
	if order.ID == "" || order.SupplierID == "" || at < 0 || len(order.Lines) == 0 {
		return errorf(ErrInvalidArgument, "invalid purchase order")
	}
	if order.OverReceiptPermille < 0 || order.PriceTolerancePermille < 0 || order.DiscountPermille < 0 || order.DiscountPermille > 1000 {
		return errorf(ErrInvalidArgument, "invalid tolerance or discount permille")
	}
	if order.PaymentPeriodSeconds < 0 || order.DiscountPeriodSeconds < 0 || order.DiscountPeriodSeconds > order.PaymentPeriodSeconds {
		return errorf(ErrInvalidArgument, "invalid payment or discount period")
	}
	seen := make(map[string]struct{}, len(order.Lines))
	for _, line := range order.Lines {
		if line.LineID == "" || line.Product == "" || line.Quantity <= 0 || line.UnitPriceCents <= 0 {
			return errorf(ErrInvalidArgument, "invalid purchase order line")
		}
		if _, exists := seen[line.LineID]; exists {
			return errorf(ErrInvalidArgument, "duplicate purchase order line: %s", line.LineID)
		}
		seen[line.LineID] = struct{}{}
	}
	return nil
}

func validateReceipt(receipt GoodsReceipt) error {
	if receipt.OrderID == "" || receipt.LineID == "" || receipt.Quantity <= 0 || receipt.Time < 0 {
		return errorf(ErrInvalidArgument, "invalid goods receipt")
	}
	return nil
}

func validateInvoice(invoice Invoice) error {
	if invoice.InvoiceID == "" || invoice.SupplierID == "" || invoice.OrderID == "" || invoice.Time < 0 || len(invoice.Lines) == 0 {
		return errorf(ErrInvalidArgument, "invalid invoice")
	}
	var amount int64
	for _, line := range invoice.Lines {
		if line.LineID == "" || line.Quantity <= 0 || line.UnitPriceCents <= 0 {
			return errorf(ErrInvalidArgument, "invalid invoice line")
		}
		lineAmount, overflow := multiplyChecked(line.Quantity, line.UnitPriceCents)
		if overflow {
			return errorf(ErrInvalidArgument, "invoice line amount overflows")
		}
		amount, overflow = addChecked(amount, lineAmount)
		if overflow {
			return errorf(ErrInvalidArgument, "invoice amount overflows")
		}
	}
	return nil
}
