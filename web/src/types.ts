export type Supplier = {
  id: string;
  supplierNumber: string;
  name: string;
  legalName?: string;
  businessNumber: string;
  representative?: string;
  status: string;
  grade?: string;
  riskLevel: string;
  supplierType?: string;
  industry?: string;
  categories: string[];
  addresses: Record<string, unknown>[];
  phone?: string;
  email?: string;
  website?: string;
  financials: Record<string, unknown>;
  bankAccount?: string;
  bankAccountUnreadable?: boolean;
  taxInfo: Record<string, unknown>;
  erpVendorId?: string;
  annualSpend: number;
  score?: number;
  metadata: Record<string, unknown>;
  createdAt: string;
  updatedAt: string;
};

export type BusinessObject = {
  id: string;
  objectType: string;
  number: string;
  supplierId?: string;
  supplierName?: string;
  parentId?: string;
  title: string;
  status: string;
  amount?: number;
  currency: string;
  startDate?: string;
  dueDate?: string;
  endDate?: string;
  riskLevel?: string;
  score?: number;
  data: Record<string, unknown>;
  createdAt: string;
  updatedAt: string;
};

export type DashboardData = {
  kpis: {
    totalSuppliers: number;
    activeSuppliers: number;
    newSuppliers: number;
    highRiskSuppliers: number;
    mediumRiskSuppliers: number;
    annualSpend: number;
    averageScore: number;
    expiringContracts: number;
    openIssues: number;
    activeContractValue: number;
    deliveryCompliance: number;
    defectRate: number;
    activeRFQ: number;
    activeRFP: number;
    overdueDeliveries: number;
    pendingApprovals: number;
    pendingScreenings: number;
  };
  topSuppliers: Pick<
    Supplier,
    "id" | "name" | "annualSpend" | "riskLevel" | "score"
  >[];
};

export type APIKey = {
  id: string;
  name: string;
  prefix: string;
  scopes: string[];
  expiresAt?: string;
  lastUsedAt?: string;
  revokedAt?: string;
  createdAt: string;
};
