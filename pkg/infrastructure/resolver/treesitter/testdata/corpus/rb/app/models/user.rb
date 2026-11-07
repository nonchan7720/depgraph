class User < ApplicationRecord
  include Taggable
  has_many :comments
  belongs_to :team, class_name: "Org::Team"

  def display_name
    "#{first} #{last}"
  end

  private

  def secret; end
end
